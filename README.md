# Tora

<p align="center">
  <img src="docs/logo.png" width="120" alt="Tora logo">
</p>

<p align="center">
  <b>Real-time sessions for chat, code, whiteboarding, and calls.</b>
</p>

<p align="center">
  A privacy-first collaboration workspace that keeps conversation, code, and context in one session.
</p>

<p align="center">
  <img src="docs/media/hero-demo.gif" width="900" alt="Tora hero demo">
</p>

<p align="center">
  <sub>Replace <code>docs/media/hero-demo.gif</code> with a 10-20 second product demo.</sub>
</p>

<p align="center">
  <a href="https://kit.svelte.dev/"><img src="https://img.shields.io/badge/SvelteKit-5.x-ff3e00?logo=svelte&logoColor=white" alt="SvelteKit"></a>
  <a href="https://www.typescriptlang.org/"><img src="https://img.shields.io/badge/TypeScript-5.x-3178c6?logo=typescript&logoColor=white" alt="TypeScript"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://redis.io/"><img src="https://img.shields.io/badge/Redis-7.x-dc382d?logo=redis&logoColor=white" alt="Redis"></a>
  <a href="https://www.scylladb.com/"><img src="https://img.shields.io/badge/ScyllaDB-supported-6cd4ff" alt="ScyllaDB"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-AGPL%20v3-blue.svg" alt="License: AGPL v3"></a>
</p>

<p align="center">
  <a href="#getting-started">Quick Start</a> •
  <a href="#features">Features</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#deployment">Deployment</a>
</p>

---

Tora is a real-time collaboration workspace where conversation, code, and visual thinking happen together. Designed to be the simplest way to connect with others securely and efficiently, it is:

- Fully open source
- Session-based 
     - rooms are ephemeral by design, with no long-term message history stored on a server
- Collaborative Tools
     - Built in code-canvas powered by monaco editor and piston, drawboard for freedraw and embeddings, taskboard to plan and manage your project, dashbaord for priority notices and notes
- Real-time: 
     - Messages, drawings, code edits, and presence updates propagate instantly between participants
- complete chat experience
     - Supports Media, files, voice messages, replies, typing indicators,emojis, reactions, typing indictaors, GIFs,   stickers, memes and more
- AI-assisted: 
      - Mention `@ToraAI` to bring an assistant into the conversation with room context
- AI-Powered Assistance
      - Auto Code Completion — Get real-time, context-aware code suggestions directly in the Monaco-powered Code Canvas, specialized for the project you're currently building.

      - Contextual Dialogue — Mention @ToraAI in chat to interact with an assistant that has full visibility of your room's conversation history and active code boards.

      - Project Assistance - Leverage ToraAI to generate, edit and manage your project

      - Private AI Mode — Request assistance or explanations that are visible only to you, allowing for private debugging or learning without interrupting the group flow.
     
- A one stop solution for small teams
      - chat, shared code editor, whiteboard, AI assistant, and call tools all live in the same room
- [Self-hostable](#deployment)

Tora uses [SvelteKit](https://kit.svelte.dev/). Collaborative editing and live synchronization are powered by [Yjs](https://yjs.dev/). Code execution is powered by [Piston](https://github.com/engineer-man/piston), [Pyodide](https://pyodide.org/), and WebContainers for in-browser Node.js execution.

## How to Use Tora

Go to the app and create a room, or join one directly via URL. Share the link with collaborators. No account is required to join; participants appear in the room instantly with a generated identity.

Everything stays in one view: chat on one side and your active board on the other. Switch between Code Canvas, whiteboard, and project board without leaving the room. Mention `@ToraAI` anywhere in chat to bring the assistant in. Start a call from the toolbar and continue working without context switching.

To collaborate privately, create a dedicated room and share that URL over your preferred secure channel.

## Features

- Real-time chat with typing indicators, reply threading, and pinned messages
- Shared code canvas with simultaneous editing and live execution output
- Collaborative whiteboard with shapes, freehand drawing, and live cursors
- WebRTC audio and video calls integrated into the workspace
- `@ToraAI` assistant with rolling room context
- Private AI mode with responses visible only to the requesting user
- Session branching to spin up child rooms from active discussions
- File uploads, image previews, and voice messages scoped to the room session
- In-browser Python execution via Pyodide web worker
- In-browser JavaScript/Node execution via WebContainers
- Presence indicators, read receipts, and per-room AI on/off control
- OAuth login (GitHub) with JWT session management
- Prometheus metrics endpoint for self-hosted deployments

## Encryptions

- WebRTC End-to-End Encryption — All real-time audio and video streams are end-to-end encrypted natively via WebRTC protocols, ensuring media remains private between participants and never reaches the server unencrypted.

- Transport Layer Security (TLS) — All data in transit, including real-time WebSocket signals for chat and drawings, is protected via Secure WebSockets (WSS) and HTTPS orchestrated through Caddy.

- JWT Integrity (HS256) — User sessions are secured using JSON Web Tokens (JWT) signed with the HMAC SHA-256 algorithm, utilizing a server-side secret to prevent token tampering.

- Timing-Attack Resistance — The authentication system employs timing-safe comparisons for cryptographic signatures to prevent attackers from guessing keys based on processing latency.

- Base64 Content Encoding — Code Canvas files and workspace attachments are Base64 encoded during transport to ensure binary data integrity across the Go API and frontend stores.

- Infrastructure Privacy — Tora’s ephemeral architecture ensures that sensitive session data is primarily held in-memory (Redis) and is designed to be wiped upon session expiry, minimizing the long-term data footprint.

** Note: Turn on E2E setting during Room Creation if you want to trade cloud stoarge for privacy. (New joinees will not be able to see previous messages in E2E setting)

## Boards

Tora's workspace is built around four boards. Each one is shared live, and everyone in the room sees the same state in real time. The app supports split views for multiple boards for convinience.

### Code Canvas

<p align="center">
  <img src="docs/media/code-canvas.gif" width="860" alt="Code Canvas">
</p>

A Monaco-powered editor where multiple participants write simultaneously. Edits are synced via Yjs CRDTs for conflict-free collaboration. Code runs directly in the workspace through sandboxed execution backends, and runnable snippets can be shared inline in chat.

### Project Management

<p align="center">
  <img src="docs/media/project-board.gif" width="860" alt="Project Management Board">
</p>

A room-scoped task board for planning and execution. Create, assign, and track work without leaving the session. Useful for sprint planning, hackathons, and live debugging coordination.

### Freedraw

<p align="center">
  <img src="docs/media/freedraw.gif" width="860" alt="Freedraw Whiteboard">
</p>

A shared whiteboard with freehand drawing, shape tools, and annotations. Live cursors show where collaborators are working. Useful for architecture diagrams, workflows, and visual brainstorming.

### Dashboard

<p align="center">
  <img src="docs/media/dashboard.gif" width="860" alt="Dashboard">
</p>

An overview of rooms, recent sessions, and activity. Create rooms, resume sessions, manage branched threads, and monitor workspace activity from one place.

<p align="center">
  <img src="docs/media/extra-1.gif" width="420" alt="Additional feature showcase">
  <img src="docs/media/extra-2.gif" width="420" alt="Additional feature showcase">
</p>

## Architecture

Tora is built for low-latency collaboration. The real-time path runs through WebSockets and Redis pub/sub; durable metadata lives in ScyllaDB; media and canvas snapshots are stored in object storage.

```text
Browser (SvelteKit + Yjs + Monaco + xterm.js)
         │
         ▼
    Go API Server
    (chi router, WebSocket hub)
         │
    ┌────┴──────────────┐
    ▼                   ▼
  Redis             ScyllaDB
(ephemeral state,   (rooms, messages,
 pub/sub, AI cache)  user metadata)
         │
         ▼
  Object Storage
  (R2 / S3 — media, canvas snapshots)
         │
         ▼
  Piston Engine
  (sandboxed code execution)
```

### Stack

- Frontend: SvelteKit, TypeScript, Tailwind CSS, Monaco Editor, Yjs, y-websocket, xterm.js, Pyodide, WebContainers
- Backend: Go, chi, gorilla/websocket, go-redis, gocql (ScyllaDB), Prometheus, JWT auth, OAuth
- Infrastructure: Docker Compose, Caddy (TLS), Prometheus, Cloudflare Workers (optional edge deployment via Wrangler)

## Getting Started

### Prerequisites

- Node.js 20+
- Go 1.24+
- Docker + Docker Compose

### 1. Clone and install

```bash
git clone https://github.com/your-org/tora
cd tora
npm install
cd backend && go mod download && cd ..
```

### 2. Start infrastructure

```bash
docker compose up -d redis piston
```

### 3. Start backend

```bash
cd backend
go run ./cmd/server/main.go
# http://localhost:8080
```

### 4. Start frontend

```bash
npm run dev
# http://localhost:5173
```

## Environment Variables

Copy `.env.example` to `.env` and configure the values for your environment.

| Variable | Description |
| --- | --- |
| `JWT_SECRET` | Secret for signing session tokens |
| `REDIS_URL` | Redis connection string |
| `SCYLLA_HOSTS` | Comma-separated ScyllaDB hosts |
| `R2_BUCKET` / `R2_ACCOUNT_ID` | Cloudflare R2 configuration |
| `PISTON_ENDPOINT` | Code execution engine URL |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` | AI provider keys (at least one required for Tora AI) |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | OAuth credentials |

## Deployment

The production topology is straightforward: the Go API server is stateless (session state lives in Redis), so horizontal scaling is direct behind a load balancer.

```text
Cloudflare / CDN
      │
      ▼
Caddy (TLS, reverse proxy)
      │
      ▼
SvelteKit Frontend   ←→   Go API Server
                               │
                    ┌──────────┼──────────┐
                    ▼          ▼          ▼
                  Redis    ScyllaDB   R2 / S3
```

Everything can be orchestrated with Docker Compose. Caddy handles automatic TLS. Configure your domain in `Caddyfile` before startup.

```bash
docker compose up -d
```

## Development

```bash
# Type-check
npm run check

# Lint
npm run lint

# Format
npm run format

# Frontend tests
npm run test

# Backend tests
cd backend && go test ./...
```

Hot reload is enabled by default in dev mode. The backend can be run with Air for live reload (`backend/.air.toml`).

## Contributing

Pull requests are welcome. For significant changes, open an issue first to discuss direction.

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/your-idea`)
3. Make changes with passing tests/lint
4. Open a pull request with a clear description of what changed and why

## License

This project is licensed under the GNU Affero General Public License v3.0 (AGPL-3.0). See [LICENSE](./LICENSE).
