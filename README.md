# Converse

[![SvelteKit](https://img.shields.io/badge/SvelteKit-5.x-ff3e00?logo=svelte&logoColor=white)](https://kit.svelte.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.x-3178c6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Redis](https://img.shields.io/badge/Redis-7.x-dc382d?logo=redis&logoColor=white)](https://redis.io/)
[![ScyllaDB](https://img.shields.io/badge/ScyllaDB-supported-6cd4ff)](https://www.scylladb.com/)

Converse is a real-time collaboration platform that combines ephemeral room-based chat, branching discussion flows, collaborative whiteboard, and a live code canvas with execution support.

It is designed for fast team sessions where context, code, and conversation stay in one workspace.

---

## Table of Contents

- [Why Converse](#why-converse)
- [Core Features](#core-features)
  - [Room Lifecycle and Access Control](#room-lifecycle-and-access-control)
  - [Real-Time Chat Engine](#real-time-chat-engine)
  - [Message Workflows and Context Actions](#message-workflows-and-context-actions)
  - [Snippet-to-Chat Workflow](#snippet-to-chat-workflow)
  - [Collaborative Code Canvas](#collaborative-code-canvas)
  - [Interactive Board Workspace](#interactive-board-workspace)
  - [Media, File Uploads, and Storage](#media-file-uploads-and-storage)
  - [Pinned Threads and Discussion Comments](#pinned-threads-and-discussion-comments)
  - [Voice and Video Calling](#voice-and-video-calling)
  - [Mobile UX and Long-Press Interaction Model](#mobile-ux-and-long-press-interaction-model)
- [Architecture Overview](#architecture-overview)
- [Tech Stack](#tech-stack)
- [Repository Structure](#repository-structure)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [1) Install Dependencies](#1-install-dependencies)
  - [2) Configure Environment Variables](#2-configure-environment-variables)
  - [3) Start Infrastructure Services](#3-start-infrastructure-services)
  - [4) Start Backend](#4-start-backend)
  - [5) Start Frontend](#5-start-frontend)
- [Environment Variables](#environment-variables)
  - [Frontend](#frontend)
  - [Backend](#backend)
- [API Surface (High-Level)](#api-surface-high-level)
- [Quality and Tooling](#quality-and-tooling)
- [Deployment Notes](#deployment-notes)
- [Security Notes](#security-notes)
- [Roadmap Ideas](#roadmap-ideas)
- [License](#license)

---

## Why Converse

Most chat tools fragment collaboration across multiple tabs and tools. Converse keeps team communication, code collaboration, and visual ideation in a single room-centric flow:

- Ephemeral and branchable room model
- Real-time messaging and room presence
- In-room code canvas with execution pipeline
- Shared visual board for non-linear collaboration
- File/media workflow backed by object storage

---

## Core Features

### Room Lifecycle and Access Control

- Create or join rooms using room name or 6-digit code
- Optional room password protection and member-aware routing
- Expiry window support for temporary/disappearing collaboration sessions
- Leave, extend, rename, delete, and promote-admin room operations
- Parent/child room relationships for branch workflows

**Media Placeholder**

```md
![Room Lifecycle Demo](docs/media/room-lifecycle.gif)
```

---

### Real-Time Chat Engine

- WebSocket-based live messaging with automatic reconnect
- Typing indicators and online member presence
- Read progress and unread anchor handling
- Message history loading and room subscription model
- Sidebar room list with activity-aware sorting

**Media Placeholder**

```md
![Real-Time Chat Demo](docs/media/realtime-chat.gif)
```

---

### Message Workflows and Context Actions

- Message-level context actions: reply, edit, delete, pin, create branch
- Inline reply preview and jump-to-source context
- Task-style message cards with checklist interactions
- Long message expansion behavior with read-more controls
- Pinned state rendering and break-room jump affordances

**Media Placeholder**

```md
![Message Actions Demo](docs/media/message-actions.gif)
```

---

### Snippet-to-Chat Workflow

- Send code snippets from the canvas to chat as structured message payloads
- Snippet + note composition flow before dispatch
- Snippet rendering as dedicated chat card with code block and note section
- Independent expand/collapse behavior for long code and long note content

**Media Placeholder**

```md
![Snippet To Chat Demo](docs/media/snippet-to-chat.gif)
```

---

### Collaborative Code Canvas

- Monaco editor workspace with project-like file tree
- Room-shared code state synchronized via Yjs/y-websocket
- Canvas snapshot load/save pipeline (Redis + optional R2 fallback)
- Terminal-like execution output stream in the canvas UI
- Runtime execution support using workers:
  - Python via Pyodide worker
  - JavaScript via dedicated JS worker

**Media Placeholder**

```md
![Code Canvas Demo](docs/media/code-canvas.gif)
```

---

### Interactive Board Workspace

- Spatial collaborative board with pan/zoom and minimap
- Free draw, erase, shapes, text boxes, sticky notes
- Insert message/media cards into board context
- Cursor presence and board-level collaboration envelopes
- Board details panel with usage and capacity indicators

**Media Placeholder**

```md
![Board Workspace Demo](docs/media/board-workspace.gif)
```

---

### Media, File Uploads, and Storage

- Media and file attachments in chat composer
- Voice message recording and upload flow
- Presigned upload endpoint and upload proxy support
- R2 object retrieval route and room-scoped file indexing
- Optional public base URL support for object delivery

**Media Placeholder**

```md
![Media Upload Demo](docs/media/media-upload.gif)
```

---

### Pinned Threads and Discussion Comments

- Pin messages inside a room
- Navigate pinned anchors
- Dedicated pinned discussion comment endpoints
- Create, edit, delete comment workflows
- Pin state propagation to room timelines

**Media Placeholder**

```md
![Pinned Discussion Demo](docs/media/pinned-discussion.gif)
```

---

### Voice and Video Calling

- Built-in room call actions from header menu
- WebRTC signaling integrated into room channel flow
- Audio/video call invite handling
- Minimized-call state and restore controls
- Call status represented in message timeline

**Media Placeholder**

```md
![Voice Video Demo](docs/media/voice-video.gif)
```

---

### Mobile UX and Long-Press Interaction Model

- Responsive split-pane behavior with mobile pane switching
- Long-press support for context menus (messages and canvas file rows)
- Native menu suppression strategies for touch interaction consistency
- Header and sidebar controls optimized for compact viewports

**Media Placeholder**

```md
![Mobile UX Demo](docs/media/mobile-ux.gif)
```

---

## Architecture Overview

```text
SvelteKit Frontend
  ├─ Chat UI / Sidebar / Composer / Board / Canvas
  ├─ Global WebSocket Client
  ├─ Monaco + Yjs collaboration
  └─ Worker-based execution (Pyodide / JS)

Go Backend (Chi Router)
  ├─ Auth + Room + Message + Upload + Canvas APIs
  ├─ WebSocket Hub (messages, presence, typing, room events)
  ├─ Room expiry cleanup worker
  └─ Usage / quota tracking

State + Persistence
  ├─ Redis (fast state, pub/sub, caches, expiry events)
  ├─ ScyllaDB/Astra (durable room/message metadata)
  └─ Cloudflare R2 (files + canvas snapshots fallback)

Execution
  └─ Optional Piston service (containerized runtime backend for extended execution scenarios)
```

---

## Tech Stack

**Frontend**
- SvelteKit + TypeScript
- Monaco Editor, xterm.js
- Yjs + y-websocket + y-monaco
- Web Workers (Pyodide and JavaScript runtime)

**Backend**
- Go (Chi router + Gorilla WebSocket)
- Redis (pub/sub, cache, expiry worker trigger)
- ScyllaDB / Astra for durable data
- Cloudflare R2 via MinIO client

**Infra**
- Docker Compose for service orchestration
- Optional Caddy reverse proxy for API domain routing

---

## Repository Structure

```text
.
├─ src/                         # SvelteKit frontend
│  ├─ lib/components/
│  │  ├─ chat/
│  │  └─ canvas/
│  ├─ lib/workers/              # Pyodide + JavaScript workers
│  └─ routes/
├─ backend/
│  ├─ cmd/server/               # Go server entrypoint
│  └─ internal/
│     ├─ handlers/
│     ├─ websocket/
│     ├─ database/
│     ├─ storage/
│     └─ router/
├─ docker-compose.yml
└─ Caddyfile
```

---

## Getting Started

### Prerequisites

- Node.js 20+
- npm 10+
- Go 1.24+
- Docker + Docker Compose (recommended for Redis/Piston/Caddy)

### 1) Install Dependencies

```bash
npm install
cd backend && go mod download && cd ..
```

### 2) Configure Environment Variables

Create a local `.env` in project root (or export variables in shell).

Use the template in [Environment Variables](#environment-variables).

### 3) Start Infrastructure Services

```bash
docker compose up -d redis piston
```

Optional (reverse proxy / TLS API host):

```bash
docker compose up -d caddy
```

### 4) Start Backend

```bash
cd backend
go run ./cmd/server/main.go
```

Backend default: `http://localhost:8080`

### 5) Start Frontend

In project root:

```bash
npm run dev
```

Frontend default: `http://localhost:5173`

---

## Environment Variables

### Frontend

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `VITE_API_BASE` | Yes | `http://localhost:8080` | Base URL for REST API calls |
| `VITE_WS_BASE` | No | derived from `VITE_API_BASE` | Explicit WebSocket base URL |
| `VITE_CHAT_DEBUG` | No | `0` | Enables client-side debug traces when `1` |

### Backend

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `APP_SECRET_KEY` | Yes | none | 32-char secret for app crypto/auth needs |
| `PORT` | No | `8080` | Backend HTTP port |
| `REDIS_ADDR` | No | `localhost:6379` | Redis host:port |
| `REDIS_PASS` | No | empty | Redis password |
| `TRUSTED_PROXIES` | No | empty | CSV of trusted proxy CIDRs/IPs |
| `SCYLLA_HOSTS` | No | `127.0.0.1` | CSV host list for local Scylla |
| `SCYLLA_KEYSPACE` | No | `converse` | Scylla keyspace |
| `ASTRA_TOKEN` | No | empty | Astra token (cloud mode) |
| `ASTRA_DB_ID` | No | empty | Astra database ID |
| `ASTRA_API_URL` | No | auto | Astra API base |
| `R2_ACCOUNT_ID` | No | auto-derivable | Cloudflare account id |
| `R2_ACCESS_KEY` / `R2_S3_access_key_id` | No | empty | R2 access key |
| `R2_SECRET_KEY` / `R2_S3_secret_access_key` | No | empty | R2 secret key |
| `R2_BUCKET` / `R2_S3_bucket_name` | No | empty | R2 bucket |
| `R2_PUBLIC_BASE_URL` | No | empty | Public URL base for uploaded objects |
| `MAX_DAILY_REQUESTS` | No | `50000` | Usage guardrail |
| `MAX_DAILY_UPLOAD_BYTES` | No | `2147483648` | Usage guardrail |
| `MAX_DAILY_BANDWIDTH_BYTES` | No | `5368709120` | Usage guardrail |
| `MAX_DAILY_MESSAGES` | No | `200000` | Usage guardrail |
| `MAX_DAILY_WS_CONNECTIONS` | No | `15000` | Usage guardrail |
| `MAX_DAILY_FILES_UPLOADED` | No | `3000` | Usage guardrail |

Example `.env` template (replace values):

```bash
# Backend
APP_SECRET_KEY=replace_with_exactly_32_chars
PORT=8080
REDIS_ADDR=localhost:6379
REDIS_PASS=
SCYLLA_HOSTS=127.0.0.1
SCYLLA_KEYSPACE=converse
TRUSTED_PROXIES=

# Optional Astra
ASTRA_TOKEN=
ASTRA_DB_ID=
ASTRA_API_URL=

# Optional R2
R2_ACCOUNT_ID=
R2_ACCESS_KEY=
R2_SECRET_KEY=
R2_BUCKET=
R2_PUBLIC_BASE_URL=

# Frontend
VITE_API_BASE=http://localhost:8080
VITE_WS_BASE=
VITE_CHAT_DEBUG=0
```

---

## API Surface (High-Level)

**Auth**
- `POST /api/auth/signup`
- `POST /api/auth/login`
- `POST /api/auth/anonymous`

**Rooms**
- `POST /api/rooms`, `POST /api/rooms/join`, `POST /api/rooms/leave`
- `POST /api/rooms/extend`, `POST /api/rooms/rename`, `POST /api/rooms/delete`
- `POST /api/rooms/break`, `POST /api/rooms/remove-member`
- `GET /api/rooms/sidebar`, `GET /api/rooms/{id}`

**Messages and Pins**
- `GET /api/rooms/{roomId}/messages`
- `POST /api/rooms/{roomId}/pins`
- `GET /api/rooms/{roomId}/pins/navigate`
- `GET/POST/PUT/DELETE /api/rooms/{roomId}/pins/{pinMessageId}/discussion/comments[...]`

**Board**
- `GET /api/rooms/{id}/board`

**Upload/Storage**
- `POST /api/upload/presigned`
- `POST /api/upload`
- `GET /api/upload/object/*`

**Canvas**
- `GET/POST /api/canvas/{roomId}/snapshot`
- `GET /api/canvas/github-archive`
- `GET /ws/canvas/{roomId}`

**WebSocket**
- `GET /ws`

---

## Quality and Tooling

Frontend:

```bash
npm run check
npm run lint
npm run test
```

Backend:

```bash
cd backend
GOCACHE=/tmp/go-build-cache go test ./...
```

---

## Deployment Notes

- Backend can run standalone (`go run` / built binary) or in Docker.
- Redis is required for core real-time behavior.
- Scylla/Astra is optional but recommended for durability.
- R2 is optional but recommended for media and snapshot persistence.
- Caddy config in this repo currently proxies `api-tora.monokenos.com` to backend.

---

## Security Notes

- Do not commit real secrets in `.env`.
- Rotate any leaked credentials immediately.
- Keep `APP_SECRET_KEY` exactly 32 chars.
- Use HTTPS/WSS in production and lock down CORS origins.
- Restrict trusted proxies via `TRUSTED_PROXIES` in production.

---

## Roadmap Ideas

- Add full API documentation (OpenAPI spec)
- Add integration and end-to-end test suites
- Add observability dashboards (metrics/traces/log correlation)
- Package frontend + backend in a single production compose profile

---

## License

Add your preferred license before publishing (for example, MIT/Apache-2.0/Proprietary) and include a `LICENSE` file.
