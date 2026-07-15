# Security Policy

## Supported Version

Security fixes target the latest `main` revision and the most recent release tag. Older development snapshots are not maintained as separate supported versions.

## Reporting

Do not open a public issue containing credentials, personal data, exploit details, or sealed evaluation content. Report suspected vulnerabilities through the repository Security tab or a private GitHub Security Advisory and include:

- affected commit and component;
- reproduction steps and expected impact;
- whether credentials or user data may be exposed;
- any known mitigation.

Rotate any exposed development credential immediately. Production deployment must use managed secrets and must never reuse the local Compose credentials documented in this repository.

## Response

The owner will confirm receipt, assess severity, prepare a regression test and fix, and publish only the minimum safe disclosure after remediation. Security changes must pass the same CI, CodeQL SARIF, dependency, secret, SBOM, and image vulnerability gates as other releases.
