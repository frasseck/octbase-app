# Project Name

A fresh project baseline.

## Overview

This repository contains a clean starting point for a multi-service application.

## Structure

- `octbase-api/` - backend service
- `octbase-frontend/` - web frontend
- `octbase-mobile/` - mobile web frontend
- `octbase-shared/` - shared frontend modules
- `docs/` - project documentation
- `scripts/` - development scripts

## Getting started

1. Copy environment template:
   - `cp .env.example .env`
2. Start local services:
   - `podman-compose up --build`
3. Open the app:
   - `http://localhost:8080`

## Development

- Backend: `cd octbase-api && go test ./...`
- Frontend: `npm run build --workspace @octbase/frontend`
- Mobile: `npm run build --workspace @octbase/mobile`

## Notes

This README intentionally avoids historical release and migration narrative.
