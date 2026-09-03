# Changelog

## 0.9.5 (2026-09-03)

### Bug Fixes

- **hoomail:** harden message delivery and rendering (6c3adbf)

### Other Changes

- **ci:** update Hoostack tool pins (#20) (617b0ee)
- **ci:** adopt HooNeedsUpdates v0.3.0 (46c4e77)
- **ci:** adopt Hoonarqube v0.3.1 (094e1a4)
- **ci:** converge released tool pins (26de1aa)

## 0.9.4 (2026-08-31)

### Bug Fixes

- **release:** embed license in Helm chart (#18) (829e33c)

## 0.9.3 (2026-08-31)

### Bug Fixes

- align Hoostack policy and release supply chain (#15) (81080bd)
- **release:** honor protected main branch (c6cf846)

### Other Changes

- standardize Hoostack dogfood (#14) (6ec986a)

## 0.9.2 (2026-08-25)

### Bug Fixes

- **store:** apply sqlite pragmas on every connection (b9ec825)
- **events:** drain sse subscribers on shutdown (1cc064d)
- **httpserver:** drop unpkg swagger ui, keep openapi.json (0d4a392)
- **mime:** treat lone cr as content and bound semantic depth (b5ec746)
- **inspect:** rfc8617 arc parsing and truncation markers (d05d7b5)
- **calendar:** rfc5545 params and utc date anchoring (a92b886)
- **smtp:** resync stream after over-limit bdat discard (b14d0c7)
- **web:** cache fetcher reuse, utc all-day rendering, single alert (cfde28a)
- **e2e:** observable selectors, port contract, tz coverage (d9fcac2)
- **chart:** raise probe timeouts to 10s (cbc88dd)

### Other Changes

- correct drift against implemented behavior (e7c1ce7)
- **release:** restore concurrency guard (ff8d7a6)

## 0.9.1 (2026-08-24)

### Bug Fixes

- **server:** enforce protocol and safety limits (deac52d)
- **web:** restore viewer and cache contracts (c0eb86e)
- **deploy:** gate releases and fix runtime dirs (de5dbae)
- **ci:** bump go toolchain to 1.26.6 (b42e801)
- **ci:** randomize e2e ports per run (7c0bf25)
- **ci:** derive e2e ports in shell (4010f6b)

### Other Changes

- align guides with implemented behavior (647a1b3)

## 0.9.0 (2026-08-11)

### Features

- **inspect:** add Mailpit parity tools (24eda7d)

### Bug Fixes

- **inspect:** scope checksum scan exceptions (2cc5a49)

## 0.8.9 (2026-08-11)

### Bug Fixes

- **viewer:** render safe embedded data images (c074462)

## 0.8.8 (2026-08-11)

### Bug Fixes

- **viewer:** fill fit preview height (686f883)

## 0.8.7 (2026-08-11)

### Bug Fixes

- **rendering:** show mixed inline images (687e483)

## 0.8.6 (2026-08-11)

### Bug Fixes

- **rendering:** restore email fidelity (7b1e32c)

## 0.8.5 (2026-08-10)

### Performance

- cut message loading overhead (c45c731)

## 0.8.4 (2026-08-10)

### Bug Fixes

- prevent reset identity races (f90ba4b)

## 0.8.3 (2026-08-10)

### Bug Fixes

- harden mail handling and UI races (e858969)
- **sendtest:** map deadline socket timeout (0e0c7f8)

## 0.8.2 (2026-07-24)

### Bug Fixes

- **web:** improve readability and email preview (1db462d)

## 0.8.1 (2026-07-24)

### Performance

- **container:** omit unused HTTP/2 code (bcb6669)

### Bug Fixes

- **web:** add application favicon (78dedc3)

## 0.8.0 (2026-07-24)

### Features

- **api:** add OpenAPI and Swagger endpoints (56b5207)

## 0.7.1 (2026-07-24)

### Bug Fixes

- **release:** tolerate Docker Hub metadata denial (4577e72)

## 0.7.0 (2026-07-24)

### Features

- **viewer:** color checks and show full source (0df98b2)

## 0.6.0 (2026-07-24)

### Features

- **viewer:** add responsive email preview (1c34f4a)

## 0.5.1 (2026-07-24)

### Bug Fixes

- **image:** publish Docker Hub repository details (b1b6683)

## 0.5.0 (2026-07-24)

### Other Changes

- document send-test encoding (1213bd3)

### Features

- **inspect:** expand offline email analysis (ff7c110)

## 0.4.2 (2026-07-23)

### Bug Fixes

- **security:** encode test email fields (5ffd872)

## 0.4.1 (2026-07-23)

### Other Changes

- document Hoomail completely (881ef3d)
- add coverage and dependency updates (b5133f6)

### Bug Fixes

- **rendering:** harden email display (86bcf48)
- **deps:** update x/net security fixes (125ccb9)

## 0.4.0 (2026-07-23)

### Features

- **helm:** add footprint-first chart (fb92eca)

## 0.3.1 (2026-07-23)

### Performance

- benchmark and optimize hot paths (372ab94)

### Bug Fixes

- **ci:** format event benchmark (65925d3)

## 0.3.0 (2026-07-23)

### Features

- add POP3 and end-to-end coverage (6cd2fcc)

## 0.2.0 (2026-07-23)

### Features

- **hoomail:** ship Go and Preact app (fe59e6c)

### Bug Fixes

- **ci:** build client before Go tests (d33c234)
- **ci:** pin compatible govulncheck (07f6f63)
- **ci:** use patched Go toolchain (35e5aff)
- **ci:** scope gosec signal (e6d1ab3)

All notable changes to Hoomail are documented in this file.

## Unreleased

### Features

- **viewer:** color inspection checks and show full raw source
