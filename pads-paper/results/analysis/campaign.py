#!/usr/bin/env python3
# Copyright 2025-2026 Daniel Seufferth
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Campaign design loading, validation, and planning for the PADS paper
(P6 paper-result bundle, P6-R1/P6-R2).

``pads-paper/results/campaign/design.json`` is the single source of truth for
the ruled initial-results campaign: the parameter entries, the labeled
recipes (R1-R8), and the labeled scenarios (A1-A8, B1-B3) in creation order.
This module is the one pure-stdlib implementation that reads it:

- the ``run-experiment.sh --design`` driver validates the design in
  preflight (before any cluster mutation) and drives the EDS API from the
  plan;
- ``analyze.py`` consumes the same plan for its deterministic label ->
  Core DB scenario mapping;
- the self-tests in ``test_acceptance.py`` and ``test_analysis.py`` import
  it (through importlib, because of the hyphenated sibling tools).

Validation contract (a design that violates any rule is refused offline):

- the top level is a JSON object with exactly the keys ``schema``,
  ``campaign``, ``model``, ``entries``, ``recipes``, ``scenarios``;
- ``schema == "pads-campaign-design-v1"``; ``model == "erlang_a_v1"``;
  ``campaign`` is a non-empty string;
- ``entries`` has exactly the four parameters ``arrival_rate``,
  ``service_rate``, ``run_duration``, ``seed_policy``; each value list is
  non-empty with **unique** integers; the first three are positive and
  ``seed_policy`` values are non-negative (the EDS entry domain);
- ``recipes`` is a non-empty list of objects with exactly the keys
  ``label``, ``arrival_rate``, ``service_rate``; labels are unique and
  both rates reference values present in the entry lists (resolvable
  recipe references);
- ``scenarios`` is a non-empty list of objects with exactly the keys
  ``label``, ``recipe``, ``priority``, ``number_of_reps``,
  ``confidence_metric``; labels are unique and of the form
  ``<uppercase letter><digits>`` (the leading letter is the experiment
  group); the recipe reference resolves to a defined recipe label;
  ``priority`` is a positive integer; ``number_of_reps`` is an integer in
  1..100000 (the EDS wire guard); ``confidence_metric`` is a finite
  number > 0 (booleans, NaN, and Inf are refused);
- ``run_duration`` and ``seed_policy`` each carry exactly one entry value
  so that every scenario bakes the same measurement window and seed base
  (the ruled campaign shape).

The plan (``plan``) derives per scenario the baked parameters from its
recipe, the experiment group from the label, and the offered load
``rho = arrival_rate / (servers * service_rate)`` with the fixed
``erlang_a_v1`` server count ``c = 2`` (the model constant documented in
``pads-paper/simulation-model/src/model.py``).

Stage durations (``stage_durations``) derive the wall-clock stage table
from one Core DB ``scenario_status`` record: created ->
translation request published -> terminal updated, in seconds (``None``
when the translation timestamp is null).

CLI:

    python3 campaign.py --design DESIGN_JSON            # validate (default)
    python3 campaign.py --design DESIGN_JSON --json     # print the plan
    python3 campaign.py --stages SCENARIO_STATUS_JSON   # stage durations

Exit codes:

    0  the design is valid (plan/stages printed when requested).
    2  a usage, input-file, or validation error (each violation printed).
"""

import argparse
import datetime
import json
import math
import re
import sys

#: The design schema version this module implements.
DESIGN_SCHEMA = "pads-campaign-design-v1"

#: The only model version the campaign design may reference.
EXPECTED_MODEL = "erlang_a_v1"

#: The fixed ``erlang_a_v1`` server count (the model constant; the
#: offered load is ``rho = arrival_rate / (SERVERS * service_rate)``).
SERVERS = 2

#: The four EDS parameter names, in creation order.
ENTRY_PARAMETERS = ("arrival_rate", "service_rate", "run_duration", "seed_policy")

#: EDS scenario-setting guards (the reference server and wire contract).
MIN_REPS = 1
MAX_REPS = 100000

#: Labeled scenarios: one uppercase letter (the experiment group) plus
#: digits, e.g. A1, A8, B3.
LABEL_PATTERN = re.compile(r"^[A-Z][0-9]+$")


class DesignError(ValueError):
    """A campaign design violates the validation contract."""


def _is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


def _is_finite_number(value):
    return (
        isinstance(value, (int, float))
        and not isinstance(value, bool)
        and math.isfinite(value)
    )


def load_design(path, raw_text=None):
    """Load and return the design JSON object from ``path``.

    Strict JSON only: duplicate keys and the non-standard ``NaN``/``Inf``
    literals are refused. Raises ``DesignError`` on any parse problem.
    """
    if raw_text is None:
        with open(path, "r", encoding="utf-8") as fh:
            raw_text = fh.read()

    def reject_constant(name):
        raise DesignError(
            "design JSON uses the non-standard constant {!r}; finite JSON "
            "numbers are required".format(name)
        )

    try:
        design = json.loads(
            raw_text, object_pairs_hook=_reject_duplicate_keys, parse_constant=reject_constant
        )
    except json.JSONDecodeError as exc:
        raise DesignError("design is not valid JSON: {}".format(exc)) from exc
    if not isinstance(design, dict):
        raise DesignError("design top level must be a JSON object")
    return design


def _reject_duplicate_keys(pairs):
    obj = {}
    for key, value in pairs:
        if key in obj:
            raise DesignError("duplicate design key: {!r}".format(key))
        obj[key] = value
    return obj


def validate_design(design):
    """Return the list of validation violations (empty when valid)."""
    violations = []
    if not isinstance(design, dict):
        return ["design top level must be a JSON object"]

    expected_keys = {"schema", "campaign", "model", "entries", "recipes", "scenarios"}
    missing = sorted(expected_keys - set(design))
    if missing:
        violations.append("design is missing keys: {}".format(missing))
    unknown = sorted(set(design) - expected_keys)
    if unknown:
        violations.append("design has unexpected keys: {}".format(unknown))
    if missing:
        return violations  # structural checks need the base keys

    if design["schema"] != DESIGN_SCHEMA:
        violations.append(
            "schema must be {!r}, got {!r}".format(DESIGN_SCHEMA, design["schema"])
        )
    if not isinstance(design["campaign"], str) or not design["campaign"]:
        violations.append("campaign must be a non-empty string")
    if design["model"] != EXPECTED_MODEL:
        violations.append(
            "model must be {!r}, got {!r}".format(EXPECTED_MODEL, design["model"])
        )

    violations.extend(_validate_entries(design["entries"]))

    entry_values = {}
    if isinstance(design["entries"], dict):
        for parameter, values in _entry_lists(design["entries"]):
            if _is_int_list(values):
                entry_values[parameter] = list(values)

    recipes = design["recipes"]
    if not isinstance(recipes, list) or not recipes:
        violations.append("recipes must be a non-empty list")
        recipe_labels = set()
    else:
        recipe_labels = set()
        for index, recipe in enumerate(recipes):
            violations.extend(
                _validate_recipe(recipe, index, entry_values, recipe_labels)
            )

    scenarios = design["scenarios"]
    if not isinstance(scenarios, list) or not scenarios:
        violations.append("scenarios must be a non-empty list")
    else:
        seen_labels = set()
        for index, scenario in enumerate(scenarios):
            violations.extend(
                _validate_scenario(scenario, index, recipe_labels, seen_labels)
            )

    # Ruled campaign shape: one measurement window and one seed base.
    if isinstance(design["entries"], dict):
        for parameter in ("run_duration", "seed_policy"):
            values = design["entries"].get(parameter)
            if _is_int_list(values) and len(values) != 1:
                violations.append(
                    "{} must carry exactly one entry value (got {})".format(
                        parameter, len(values)
                    )
                )
    return violations


def _entry_lists(entries):
    """Yield (parameter, value-list) for the four entry parameters."""
    if not isinstance(entries, dict):
        return []
    for parameter in ENTRY_PARAMETERS:
        if parameter in entries:
            yield parameter, entries[parameter]


def _validate_entries(entries):
    violations = []
    if not isinstance(entries, dict):
        return ["entries must be a JSON object"]
    missing = sorted(set(ENTRY_PARAMETERS) - set(entries))
    if missing:
        violations.append("entries is missing parameters: {}".format(missing))
    unknown = sorted(set(entries) - set(ENTRY_PARAMETERS))
    if unknown:
        violations.append("entries has unknown parameters: {}".format(unknown))
    for parameter in ENTRY_PARAMETERS:
        if parameter not in entries:
            continue
        values = entries[parameter]
        if not isinstance(values, list) or not values:
            violations.append(
                "entry {} must be a non-empty list of values".format(parameter)
            )
            continue
        if not _is_int_list(values):
            violations.append(
                "entry {} values must all be integers (got {!r})".format(
                    parameter, [v for v in values if not _is_int(v)]
                )
            )
            continue
        duplicates = sorted({v for v in values if values.count(v) > 1})
        if duplicates:
            violations.append(
                "entry {} has duplicate values: {}".format(parameter, duplicates)
            )
        if parameter != "seed_policy":
            for value in values:
                if _is_int(value) and value <= 0:
                    violations.append(
                        "entry {} value {} must be positive".format(parameter, value)
                    )
        else:
            for value in values:
                if _is_int(value) and value < 0:
                    violations.append(
                        "entry seed_policy value {} must be >= 0".format(value)
                    )
    return violations


def _is_int_list(values):
    return isinstance(values, list) and all(_is_int(v) for v in values)


def _validate_recipe(recipe, index, entry_values, recipe_labels):
    violations = []
    prefix = "recipe {}".format(index)
    if not isinstance(recipe, dict):
        return ["{}: must be a JSON object".format(prefix)]
    expected = {"label", "arrival_rate", "service_rate"}
    missing = sorted(expected - set(recipe))
    if missing:
        violations.append("{}: missing keys: {}".format(prefix, missing))
    unknown = sorted(set(recipe) - expected)
    if unknown:
        violations.append("{}: unexpected keys: {}".format(prefix, unknown))
    if missing:
        return violations
    label = recipe["label"]
    if not isinstance(label, str) or not label:
        violations.append("{}: label must be a non-empty string".format(prefix))
    else:
        if label in recipe_labels:
            violations.append("{}: duplicate recipe label {!r}".format(prefix, label))
        recipe_labels.add(label)
    for field in ("arrival_rate", "service_rate"):
        value = recipe[field]
        if not _is_int(value):
            violations.append(
                "{}: {} must be an integer, got {!r}".format(prefix, field, value)
            )
        elif field in entry_values and value not in entry_values[field]:
            violations.append(
                "{}: {} value {} does not resolve to an entry value".format(
                    prefix, field, value
                )
            )
    return violations


def _validate_scenario(scenario, index, recipe_labels, seen_labels):
    violations = []
    prefix = "scenario {}".format(index)
    if not isinstance(scenario, dict):
        return ["{}: must be a JSON object".format(prefix)]
    expected = {"label", "recipe", "priority", "number_of_reps", "confidence_metric"}
    missing = sorted(expected - set(scenario))
    if missing:
        violations.append("{}: missing keys: {}".format(prefix, missing))
    unknown = sorted(set(scenario) - expected)
    if unknown:
        violations.append("{}: unexpected keys: {}".format(prefix, unknown))
    if missing:
        return violations
    label = scenario["label"]
    if not isinstance(label, str) or not LABEL_PATTERN.match(label or ""):
        violations.append(
            "{}: label must match {} (e.g. A1, B3), got {!r}".format(
                prefix, "an uppercase letter followed by digits", label
            )
        )
    else:
        if label in seen_labels:
            violations.append("{}: duplicate scenario label {!r}".format(prefix, label))
        seen_labels.add(label)
    recipe = scenario["recipe"]
    if not isinstance(recipe, str) or recipe not in recipe_labels:
        violations.append(
            "{}: recipe {!r} does not reference a defined recipe label".format(
                prefix, recipe
            )
        )
    priority = scenario["priority"]
    if not _is_int(priority) or priority <= 0:
        violations.append(
            "{}: priority must be a positive integer, got {!r}".format(prefix, priority)
        )
    reps = scenario["number_of_reps"]
    if not _is_int(reps) or not (MIN_REPS <= reps <= MAX_REPS):
        violations.append(
            "{}: number_of_reps must be an integer in {}..{}, got {!r}".format(
                prefix, MIN_REPS, MAX_REPS, reps
            )
        )
    metric = scenario["confidence_metric"]
    if not _is_finite_number(metric) or metric <= 0:
        violations.append(
            "{}: confidence_metric must be a finite number > 0, got {!r}".format(
                prefix, metric
            )
        )
    return violations


def plan(design):
    """Return the normalized campaign plan (raises ``DesignError`` on an
    invalid design).

    Shape::

        {
          "entries": {parameter: [values in creation order]},
          "recipes": [
            {"label", "arrival_rate", "service_rate", "rho"}, ...
          ],
          "scenarios": [
            {"label", "group", "recipe", "priority", "number_of_reps",
             "confidence_metric", "arrival_rate", "service_rate",
             "run_duration", "seed_policy", "rho"},
            ... in creation order
          ]
        }
    """
    violations = validate_design(design)
    if violations:
        raise DesignError("; ".join(violations))

    entries = {p: list(design["entries"][p]) for p in ENTRY_PARAMETERS}
    run_duration = entries["run_duration"][0]
    seed_policy = entries["seed_policy"][0]

    recipe_by_label = {
        recipe["label"]: recipe for recipe in design["recipes"]
    }
    planned_recipes = []
    for recipe in design["recipes"]:
        planned_recipes.append(
            {
                "label": recipe["label"],
                "arrival_rate": recipe["arrival_rate"],
                "service_rate": recipe["service_rate"],
                "rho": offered_load(recipe["arrival_rate"], recipe["service_rate"]),
            }
        )
    planned_scenarios = []
    for scenario in design["scenarios"]:
        recipe = recipe_by_label[scenario["recipe"]]
        planned_scenarios.append(
            {
                "label": scenario["label"],
                "group": scenario["label"][0],
                "recipe": scenario["recipe"],
                "priority": scenario["priority"],
                "number_of_reps": scenario["number_of_reps"],
                "confidence_metric": scenario["confidence_metric"],
                "arrival_rate": recipe["arrival_rate"],
                "service_rate": recipe["service_rate"],
                "run_duration": run_duration,
                "seed_policy": seed_policy,
                "rho": offered_load(recipe["arrival_rate"], recipe["service_rate"]),
            }
        )
    return {
        "entries": entries,
        "recipes": planned_recipes,
        "scenarios": planned_scenarios,
    }


def offered_load(arrival_rate, service_rate):
    """Return ``rho = arrival_rate / (SERVERS * service_rate)``."""
    return arrival_rate / (SERVERS * service_rate)


def stage_durations(record):
    """Return the wall-clock stage durations for one Core DB record.

    Stages (seconds): ``created -> translation_request_published_at``,
    ``translation_request_published_at -> updated_at``, and the total
    ``created -> updated_at``. A null translation timestamp yields ``None``
    for the two translation-relative stages while the total is still
    computed. Raises ``DesignError`` on a malformed or out-of-order
    timestamp set.
    """
    if not isinstance(record, dict):
        raise DesignError("scenario record must be a JSON object")

    def parse(name):
        value = record.get(name)
        if value is None:
            return None
        if not isinstance(value, str):
            raise DesignError(
                "scenario record field {} must be a string timestamp, got {!r}".format(
                    name, value
                )
            )
        try:
            return datetime.datetime.fromisoformat(value)
        except ValueError as exc:
            raise DesignError(
                "scenario record field {} is not an ISO-8601 timestamp: "
                "{!r}".format(name, value)
            ) from exc

    created = parse("created_at")
    updated = parse("updated_at")
    if created is None or updated is None:
        raise DesignError("scenario record needs created_at and updated_at")
    translated = parse("translation_request_published_at")

    def seconds(start, end):
        return (end - start).total_seconds()

    if seconds(created, updated) < 0:
        raise DesignError(
            "scenario record updated_at precedes created_at".format(record.get("id"))
        )
    if translated is not None and seconds(created, translated) < 0:
        raise DesignError(
            "scenario record translation_request_published_at precedes created_at"
        )
    if (
        translated is not None
        and seconds(translated, updated) < 0
    ):
        raise DesignError(
            "scenario record updated_at precedes translation_request_published_at"
        )
    return {
        "created_to_translation_published_s": (
            None if translated is None else seconds(created, translated)
        ),
        "translation_published_to_terminal_s": (
            None if translated is None else seconds(translated, updated)
        ),
        "total_s": seconds(created, updated),
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Validate a PADS campaign design (P6-R1) or print its plan."
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "--design", help="path to the campaign design JSON (validation/plan)"
    )
    mode.add_argument(
        "--stages", help="path to an exported scenario-status JSON (stage durations)"
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="with --design: print the normalized plan as JSON instead of a check line",
    )
    args = parser.parse_args(argv)

    if args.stages is not None:
        try:
            with open(args.stages, "r", encoding="utf-8") as fh:
                records = json.load(fh)
        except (OSError, ValueError, json.JSONDecodeError) as exc:
            sys.stderr.write("campaign: cannot read scenario-status file: {}\n".format(exc))
            return 2
        if not isinstance(records, list):
            sys.stderr.write(
                "campaign: scenario-status file must be a JSON array of records\n"
            )
            return 2
        try:
            stages = {
                str(record["id"]): stage_durations(record)
                for record in records
                if isinstance(record, dict) and _is_int(record.get("id"))
            }
        except DesignError as exc:
            sys.stderr.write("campaign: {}\n".format(exc))
            return 2
        if len(stages) != len(records):
            sys.stderr.write(
                "campaign: scenario-status records must carry integer ids\n"
            )
            return 2
        sys.stdout.write(json.dumps(stages, indent=2) + "\n")
        return 0

    if args.design is None:
        sys.stderr.write("campaign: one of --design or --stages is required\n")
        return 2

    try:
        design = load_design(args.design)
    except (OSError, DesignError) as exc:
        sys.stderr.write("campaign: {}\n".format(exc))
        return 2

    violations = validate_design(design)
    if violations:
        for violation in violations:
            sys.stderr.write("campaign: VIOLATION {}\n".format(violation))
        sys.stderr.write(
            "campaign: design {} is invalid ({} violation(s))\n".format(
                args.design, len(violations)
            )
        )
        return 2

    if args.json:
        sys.stdout.write(json.dumps(plan(design), indent=2) + "\n")
    else:
        planned = plan(design)
        sys.stdout.write(
            "campaign: design OK - {} entries, {} recipes, {} scenarios "
            "({} campaign, model {})\n".format(
                sum(len(v) for v in planned["entries"].values()),
                len(planned["recipes"]),
                len(planned["scenarios"]),
                design["campaign"],
                design["model"],
            )
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
