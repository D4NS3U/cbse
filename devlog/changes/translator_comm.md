# Translator-Scenario Manager-Communication

## Mission statement

This is a specification of Translator-Scenario Manager-Communication implementations.
Simply put, this code part implements the communication handshake between *Scenario Manager (SM)* and *Translator*. 
It uses the same JetStream-Server as implemented in the *EDS* communication part of the code.

## Location

The main logic for this part should be in the trans_com.go file located in:

> cbse/scenario-manager/internal/nats/trans_com.go

## Specification

### Main Objective

This part of the code should provide the communciation interface between the *SM* and the *Translator* using NATS/JetStream. The Code should be light-weight and robust.

### Startup

This process is started by a method of *SM* currently not implemented. Basic information for this method is the following:
- monitoring available resources
- selecting entries in the scenario status database and starting sub-process based on the current state of this scenario

### Communication process

1. *SM* grabs a scenario from the scenario status database which is in the **created** state and locks it for other instances.
    1. Currently, the default code inserts **pending** as the state, which should be changed. See [Scenario State Transition](#scenario-state-transitions).
    2. Currently, the logic, when to grab a scenario is not necessary. It should therefore be a modular placeholder code-brick, that can be replaced with a sophisticated logic later on.
    3. The locking is necessary to make sure that the scenario is not consumed by another Scenario Manager process
        1. The row is claimed with SELECT ... FOR UPDATE SKIP LOCKED , we update state from **created** to **translating**, commit, the release the DB lock before publishing
        2. A sophisticated unlocking logic should be worked out; naturally, it should be unlocked in Step 7; Possibly, the *SM* should publish a first heartbeat request to *Translator* after 60 seconds, and a second heartbeat-request after another 60 seconds. 
2. *SM* creates the message-payload which consists of the following information found in the scenario status database:
    - id
    - project-id
    - recipe info
    - confidence metric
    1. The structure of the message-payload reflects the information in the scenario status database:
        - fields: id, project-id, recipe-info, confidence-metric
3. *SM* publishes the message to cbse.<project-name>.trans.request
    1. The *Translator* knows about <project-name> similarly as the *EDS* knows about <project-name>: via SimulationExperiment.metadata.name. If it is still unclear of how the *Translator* can consume this message, then we have to think about providing the *Translator* with the information in SimulationExperiment.metadata.name
4. *Translator* consumes the message, if possible (see comment above)
5. *Translator* processes the message with its internal logic (no implementation needed here currently!!)
6. *Translator* publishes a message to *SM* via cbse.<project-name>.trans.<id>.ready
    1. Message body currently is not completely ready, as it is unsure, when the container gets created, but it should possibly happen on *Translator* side, therefore the planned body looks like this:
        - id (taken from the message of *SM*)
        - project-id (taken from the message of *SM*)
        - container-image (with the url to the container-image)
7. *SM* consumes the message and moves the status of the scenario from **created** to **scheduled** and unlocks the database-entry in the scenario status database

### Scenario State transitions

Planned state transitions:
1. **Created**
2. **Translating**
3. **Scheduled**
4. **Starting Runners**
5. **In Processing**
6. **In PreProcessing**
7. **Finished**

### Failures

*Translator* can be:
 - **failed** => it gets reset by *Experiment Operator*
 - **unresponsive**; this can have multiple causes, e.g.:
    - faulty *Translator* internal logic => crashes should be self-healable via K8s, as *Translator* pod fails; other issues (logic loops, etc.) are not catchable
    - faulty connection due to NATS/JetStream => NATS/JetStream selfheals due to Kubernetes self-hezaling?
    - staling due to high demand => *Translator* needs to be autoscalable (does this have consequences on internal logic?)

### Methods

1. selectScenarioTrans(): Method for selecting a scenario for Translation; rudimentary and modular for future changes; localized in scenario-manager/internal/core/selector.go
2. createReadyMessageTrans(): Used to create the message-payload *SM* publishes to  


### Missing Parts 


