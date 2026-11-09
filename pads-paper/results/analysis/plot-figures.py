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

"""Deterministic SVG figure rendering for the PADS paper (P6-R3).

Reads the campaign aggregates produced by ``analyze.py``
(``analysis.json`` in the sanitized evidence directory) and renders
exactly four SVG figures:

- F1 ``figures/f1-mean-wait-vs-rho.svg``: ``mean_wait_time`` vs offered
  load rho with 95% CI error bars, one series per service rate mu;
- F2 ``figures/f2-abandonment-vs-rho.svg``: ``abandonment_probability``
  vs rho with 95% CI error bars (Experiment-A and Experiment-B markers);
- F3 ``figures/f3-throughput-utilization-vs-rho.svg``: mean throughput
  and mean server utilization vs rho (two stacked subplots, Experiment-A
  and Experiment-B markers);
- F4 ``figures/f4-reps-rounds-per-scenario.svg``: final replications and
  runner rounds per Experiment-A scenario.

SVG output is deterministic: a fixed ``svg.hashsalt`` (stable element
ids), fixed figure geometry, fixed data order (design creation order,
rho-sorted where x is rho), and no timestamp or machine-specific
metadata, so regenerating the figures from identical aggregates is
byte-stable. The only third-party dependency is matplotlib, pinned in
``requirements-analysis.txt``.

CLI:

    python3 plot-figures.py --analysis ANALYSIS_JSON [--out FIGURES_DIR]

``--out`` defaults to ``<ANALYSIS_JSON dir>/figures``.

Exit codes:

    0  exactly four SVG figures written.
    1  the aggregates are inconsistent or a figure failed to render.
    2  usage or input-file error.
    3  matplotlib is not importable.
"""

import argparse
import json
import os
import sys

#: Fixed salt for the SVG element ids (deterministic regeneration).
SVG_HASHSALT = "pads-campaign-v1"
# Fixed dc:date metadata so repeated renders are byte-identical
# (matplotlib embeds the render time otherwise).
FIGURE_DATE = "2026-01-01T00:00:00+00:00"

FIGURE_FILES = (
    "f1-mean-wait-vs-rho.svg",
    "f2-abandonment-vs-rho.svg",
    "f3-throughput-utilization-vs-rho.svg",
    "f4-reps-rounds-per-scenario.svg",
)


class PlotError(Exception):
    """The aggregates are inconsistent or rendering failed."""


def load_analysis(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            analysis = json.load(fh)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        raise PlotError("cannot read analysis JSON: {}".format(exc)) from exc
    if not isinstance(analysis, dict) or not isinstance(analysis.get("scenarios"), list):
        raise PlotError("analysis JSON must carry a scenario list")
    if not analysis["scenarios"]:
        raise PlotError("analysis JSON carries no scenarios")
    return analysis


def _setup_matplotlib():
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    # Deterministic SVG output: stable element ids, no per-run variation.
    plt.rcParams["svg.hashsalt"] = SVG_HASHSALT
    plt.rcParams["svg.fonttype"] = "path"
    # Fixed geometry for all figures.
    plt.rcParams["figure.figsize"] = (7.0, 4.5)
    plt.rcParams["savefig.dpi"] = 150
    plt.rcParams["axes.titleweight"] = "bold"
    return plt


def _by_rho(records):
    return sorted(records, key=lambda r: (r["rho"], r["label"]))


def _savefig(fig, path):
    """Deterministic SVG save: fixed metadata date, fixed format."""
    fig.savefig(path, format="svg", metadata={"Date": FIGURE_DATE})


def figure_mean_wait(plt, analysis, out_dir):
    """F1: mean_wait_time vs rho with 95% CI error bars, one series per mu."""
    records = analysis["scenarios"]
    mus = sorted({r["service_rate"] for r in records})
    fig, ax = plt.subplots()
    styles = ["o-", "s-", "^-", "D-"]
    for mu, style in zip(mus, styles):
        series = [r for r in _by_rho(records) if r["service_rate"] == mu]
        rho = [r["rho"] for r in series]
        mean = [r["mean_wait_time"] for r in series]
        err = [r["mean_wait_time_half_width_95"] for r in series]
        labels = [r["label"] for r in series]
        ax.errorbar(
            rho,
            mean,
            yerr=err,
            marker=style[0],
            linestyle=style[1],
            capsize=3,
            capthick=1.0,
            elinewidth=1.0,
            markersize=5,
            linewidth=1.2,
            label=r"$\mu={}$".format(mu),
        )
        for x, y, label in zip(rho, mean, labels):
            ax.annotate(label, (x, y), textcoords="offset points", xytext=(0, 8),
                        ha="center", fontsize=7)
    ax.set_xlabel(r"offered load $\rho = \lambda / (c\mu)$, $c=2$")
    ax.set_ylabel(r"$\mathrm{mean\_wait\_time}$ (simulation time units)")
    ax.set_title("F1: Mean waiting time vs offered load (95% CI)")
    ax.grid(True, linewidth=0.4, alpha=0.6)
    ax.legend(fontsize=8)
    fig.tight_layout()
    path = os.path.join(out_dir, FIGURE_FILES[0])
    _savefig(fig, path)
    plt.close(fig)
    return path


def _experiment_markers(records, marker_a="o", marker_b="s"):
    """Yield (x list, y list, err list, marker, label) per experiment."""
    groups = {}
    for record in _by_rho(records):
        groups.setdefault(record["group"], []).append(record)
    series = []
    for group in sorted(groups):
        entries = groups[group]
        series.append(
            (
                [r["rho"] for r in entries],
                [r["abandonment_probability_mean"] for r in entries],
                [r["abandonment_probability_half_width_95"] for r in entries],
                marker_a if group == "A" else marker_b,
                "Experiment {}".format(group),
            )
        )
    return series


def figure_abandonment(plt, analysis, out_dir):
    """F2: abandonment_probability vs rho with 95% CI error bars."""
    fig, ax = plt.subplots()
    for x, y, err, marker, label in _experiment_markers(analysis["scenarios"]):
        ax.errorbar(
            x,
            y,
            yerr=err,
            marker=marker,
            linestyle="None",
            capsize=3,
            capthick=1.0,
            elinewidth=1.0,
            markersize=5,
            linewidth=1.2,
            label=label,
        )
    ax.set_xlabel(r"offered load $\rho = \lambda / (c\mu)$, $c=2$")
    ax.set_ylabel(r"$\mathrm{abandonment\_probability}$ (replication mean)")
    ax.set_title("F2: Abandonment probability vs offered load (95% CI)")
    ax.grid(True, linewidth=0.4, alpha=0.6)
    ax.legend(fontsize=8)
    fig.tight_layout()
    path = os.path.join(out_dir, FIGURE_FILES[1])
    _savefig(fig, path)
    plt.close(fig)
    return path


def figure_throughput_utilization(plt, analysis, out_dir):
    """F3: mean throughput and mean server utilization vs rho (stacked)."""
    records = _by_rho(analysis["scenarios"])

    def subplot(ax, values, ylabel, title, marker_a="o", marker_b="s"):
        groups = {}
        for record in records:
            groups.setdefault(record["group"], []).append(record)
        for group in sorted(groups):
            entries = groups[group]
            ax.plot(
                [r["rho"] for r in entries],
                [r[values] for r in entries],
                marker=marker_a if group == "A" else marker_b,
                linestyle="-",
                markersize=5,
                linewidth=1.2,
                label="Experiment {}".format(group),
            )
        ax.set_xlabel(r"offered load $\rho = \lambda / (c\mu)$, $c=2$")
        ax.set_ylabel(ylabel)
        ax.set_title(title)
        ax.grid(True, linewidth=0.4, alpha=0.6)
        ax.legend(fontsize=8)

    fig, (ax_top, ax_bottom) = plt.subplots(2, 1)
    subplot(
        ax_top,
        "throughput_mean",
        r"$\mathrm{throughput}$ (replication mean)",
        "F3a: Mean throughput vs offered load",
    )
    subplot(
        ax_bottom,
        "server_utilization_mean",
        r"$\mathrm{server\_utilization}$ (replication mean)",
        "F3b: Mean server utilization vs offered load",
    )
    fig.tight_layout()
    path = os.path.join(out_dir, FIGURE_FILES[2])
    _savefig(fig, path)
    plt.close(fig)
    return path


def figure_reps_rounds(plt, analysis, out_dir):
    """F4: final replications and runner rounds per A scenario."""
    records = [r for r in analysis["scenarios"] if r["group"] == "A"]
    if not records:
        raise PlotError("no Experiment-A scenarios for F4")
    fig, ax = plt.subplots()
    x = range(len(records))
    width = 0.4
    bars = ax.bar(
        [i - width / 2 for i in x],
        [r["n"] for r in records],
        width=width,
        label="final replications (n)",
    )
    ax.bar(
        [i + width / 2 for i in x],
        [r["runner_round"] for r in records],
        width=width,
        label="runner rounds",
    )
    ax.set_xticks(list(x))
    ax.set_xticklabels([r["label"] for r in records], fontsize=8)
    ax.set_ylabel("count")
    ax.set_title("F4: Final replications and runner rounds per A scenario")
    ax.grid(True, axis="y", linewidth=0.4, alpha=0.6)
    ax.legend(fontsize=8)
    for bar in bars:
        height = bar.get_height()
        if height:
            ax.annotate(
                str(int(height)),
                (bar.get_x() + bar.get_width() / 2, height),
                textcoords="offset points",
                xytext=(0, 3),
                ha="center",
                fontsize=7,
            )
    fig.tight_layout()
    path = os.path.join(out_dir, FIGURE_FILES[3])
    _savefig(fig, path)
    plt.close(fig)
    return path


def render_figures(analysis, out_dir):
    """Render all four figures into ``out_dir``; return the file paths."""
    plt = _setup_matplotlib()
    os.makedirs(out_dir, exist_ok=True)
    paths = [
        figure_mean_wait(plt, analysis, out_dir),
        figure_abandonment(plt, analysis, out_dir),
        figure_throughput_utilization(plt, analysis, out_dir),
        figure_reps_rounds(plt, analysis, out_dir),
    ]
    missing = [p for p in paths if not os.path.isfile(p)]
    if missing:
        raise PlotError("figures missing after render: {}".format(missing))
    return paths


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Render the four deterministic PADS campaign SVG figures (P6-R3)."
    )
    parser.add_argument(
        "--analysis", required=True, help="path to analysis.json (from analyze.py)"
    )
    parser.add_argument(
        "--out",
        help="figures output directory (default: <analysis dir>/figures)",
    )
    args = parser.parse_args(argv)

    try:
        analysis = load_analysis(args.analysis)
    except PlotError as exc:
        sys.stderr.write("plot-figures: {}\n".format(exc))
        return 2

    out_dir = args.out or os.path.join(os.path.dirname(os.path.abspath(args.analysis)), "figures")
    try:
        render_figures(analysis, out_dir)
    except PlotError as exc:
        sys.stderr.write("plot-figures: {}\n".format(exc))
        return 1
    except ImportError as exc:
        sys.stderr.write(
            "plot-figures: matplotlib is required (pinned in "
            "requirements-analysis.txt): {}\n".format(exc)
        )
        return 3
    for name in FIGURE_FILES:
        sys.stdout.write("plot-figures: wrote {}\n".format(os.path.join(out_dir, name)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
