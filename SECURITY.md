# Security Policy

## Supported versions

CBSE is a research prototype without a formal release cadence. Until a support
policy is adopted, the only supported version is the current state of the
`main` branch; earlier milestones recorded in `CHANGELOG.md` are research
checkpoints, not supported releases.

## Reporting a vulnerability

Security reports are accepted via **GitHub's built-in private vulnerability
reporting**: open the repository's **Security** tab on GitHub and select
**"Report a vulnerability"** to file a private report. The maintainers see
and respond to reports privately on GitHub.

When reporting, please include:

- A description of the vulnerability and its impact.
- Steps to reproduce, or the affected component and code path.
- The component involved (`experiment-operator`, `scenario-manager`,
  `component-templates/translator`, or the test harness) and the commit or
  branch on which you observed the issue.

## Scope

This policy covers the code and documentation in this repository. Out of scope:

- Vulnerabilities of upstream dependencies — report those to their respective
  upstream projects.
- The cluster tier (smoke/e2e test infrastructure): it runs on **user-operated
  infrastructure — your Kubernetes cluster and your container registry**.
  Misconfiguration of that infrastructure is the operator's responsibility,
  not a product vulnerability.

## Response expectations

As a research project without a release cadence, response times are not
guaranteed. Maintainers will acknowledge private reports as soon as practicable
and will coordinate a fix or mitigation before any public disclosure.
