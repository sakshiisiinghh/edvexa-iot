# Build the initial scaffold for Edvexa's IoT Device Monitoring assignment using Go, PostgreSQL, and Docker Compose: Go module, chi HTTP server, pgxpool Postgres connectivity, /healthz and /readyz endpoints, log/slog structured logging, Postgres service with Docker health check in docker-compose.yml, graceful shutdown and HTTP timeouts, Makefile, .env.example, .gitignore, basic automated tests, and docs for running locally. No telemetry, alerts, WebSockets, auth, frontend, Redis.

Plan: `6acf7fb3da84b55fbd162c3c`
Repositories: sakshiisiinghh/edvexa-iot

## Tasks

- [x] Initialize Go module and dependencies
- [x] Environment-driven configuration package
- [x] PostgreSQL connection pool
- [x] chi router, slog request logging and health endpoints
- [x] Entrypoint wiring with graceful shutdown and HTTP timeouts
- [ ] docker-compose.yml with PostgreSQL health check
- [ ] Makefile, .env.example and .gitignore
- [ ] Automated tests
- [ ] README with local run instructions and testing strategy

---

Written by code0 when the plan was approved. The plan itself lives outside this
repository — see `~/.code0/plans/` — because a plan can span repositories and is
not a source file. This note is the thread back to it.
