# Container-Based Simulation Environment (CBSE)

The **Container-Based Simulation Environment (CBSE)** integrates large-scale simulation workflows with **Kubernetes-native infrastructure**.

CBSE currently consists of three core components:

- **Experiment Operator (ExOp)** – Kubernetes operator managing simulation experiments  
- **SimulationExperiment CR** – Custom Resource definition for experiment configuration  
- **Scenario Manager (SM)** – Backend service coordinating scenario execution and lifecycle management  

---

## Project Status (dev branch)

🚧 **Work in Progress**

This branch reflects ongoing development.

- The **Experiment Operator** successfully manages `SimulationExperiment` resources, but does not yet provide full execution functionality.
- The **Scenario Manager** is under active development and not yet production-ready.

Interfaces and functionality may change.

---

## Publications

Related research and conceptual foundations:

| Publication | Conference | Date |
|-------------|------------|------|
| *Towards Container-Based Simulation: A Concept For A Distributed And Scalable Simulation Framework* | 12th Simulation Workshop | April 2025 |
| *Container-Based Simulation: A Concept For Large-Scale Simulation Environments* | 27. ASIM Symposium Simulationstechnik | January 2024 |
| *Using Kubernetes to Improve Data Farming Capabilities* | 2023 Winter Simulation Conference | December 2023 |
| *On the Usage of Containers and Container Orchestrators as a Computational Infrastructure for Simulation Experiments* | 20. ASIM Fachtagung Produktion und Logistik | September 2023 |

More: [ResearchGate Profile](https://www.researchgate.net/profile/Daniel-Seufferth/research)

---

## License

Licensed under the **Apache License 2.0**. See `LICENSE` for details.
