# Specification for a Basic Scenario Selection Logic
This Document has the following structure:

1. Mission Statement -- Explains the intent and attitude of the work
2. Scope -- Explains the boundaries of the work; the goals, changes and scope in a general way
3. Change Location -- Describes the locations and files that need to be changed or created
4. Logic Description -- Describes the logic implemented in the newly created or changed files


## Mission Statement
This specification describes the first basic scenario selection logic for the Scenario Manager.

The goal is to add a simple, reliable first version of the decision step that chooses which stored scenario should be processed next. This is not meant to be the final or most intelligent scheduling logic. It is meant to create a clear starting point that works, is easy to understand, and can later be replaced by more advanced selection strategies.

The implementation should favor clarity over cleverness. The AI coding agent should keep the logic small, explicit, modular, and close to the existing Scenario Manager structure. It should reuse the current translator handoff workflow instead of redesigning it, avoid advanced scheduling behavior, and leave the code in a shape where future scenario selection logic can be swapped in without disturbing EDS ingestion or Translator communication.


## Scope
This change adds the first basic scenario selection logic to the Scenario Manager.

Today, the Scenario Manager can receive scenarios from EDS and store them in the Core DB. It also already has a translator handoff workflow that can process one specific scenario when it is given a scenario ID. What is still missing is the simple decision step in between: choosing which stored scenario should be handled next.

The scope of this specification is to describe that first decision step. The Scenario Manager should be able to find scenarios that are ready for translation, select one suitable candidate, and pass it into the existing translator handoff flow. This first version is intentionally simple and predictable. It is meant to prove the basic workflow and create a clean place where more advanced selection logic can be added later.

The selection logic should focus only on scenarios that are in the `Created` state. These are scenarios that have been received and stored, but have not yet been sent to the Translator. Once a scenario is selected, the existing translator handoff is responsible for claiming it, publishing the translation request, and updating the scenario state.

This change should not introduce advanced scheduling. It should not try to optimize cluster resources, balance load between projects, batch multiple scenarios, or make decisions based on simulation runtime estimates. Those topics are intentionally left for later versions of the scenario selection logic.

The result of this change should be a small, modular selection component that can be called by the Scenario Manager and later replaced or extended without changing the rest of the translation workflow.

 ## Change Location


 ## Logic Description

