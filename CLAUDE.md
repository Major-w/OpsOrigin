# CLAUDE.md - AI Coding Guide for OpsOrigin

## Project Overview

OpsOrigin is an AI-native operations platform with a Go backend and React frontend.

## Tech Stack

### Frontend (`web/`)
- **Build**: Vite 6 + React 18 + React Router 6
- **UI**: antd 6 + @ant-design/pro-components 2.8
- **State**: zustand 4
- **Styling**: Tailwind CSS 3 + CSS variables
- **Language**: TypeScript 5

### Backend (`cmd/core/`, `internal/`)
- **Language**: Go 1.26 (stdlib net/http, Go 1.22 ServeMux)
- **ORM**: ent + PostgreSQL
- **Cache**: Redis
- **API Docs**: Swagger/OpenAPI 3.0 (`docs/swagger/`)

## Key Conventions

### API Calls
- `apiFetch(path)` and `apiFetchRaw(path)` auto-prepend `/api/v1`
- Page code must NOT include `/api/v1` in the path
- Example: `apiFetch('/scripts')` → hits `/api/v1/scripts`

### Component Patterns
- Use `plain <button>` for icon-only buttons (antd `type="text"` CSS-in-Js overrides Tailwind)
- Import `getMessage()` from `@/lib/message` for notifications (singleton pattern)
- Use `ProTable` from `@ant-design/pro-components` for all table pages
- Use `StatisticCard`/`StatisticCardGroup` for stat cards

### Styling
- Theme: 经典蓝 `colorPrimary: '#3B82F6'`
- Dark mode via `darkMode: 'class'`
- CSS variables defined in `web/src/index.css`
- Avoid hardcoded hex colors — use CSS variables

### Routing
- Routes defined in `web/src/App.tsx` (React Router 6)
- Dashboard layout wraps all `/dashboard/*` routes
- Route names mapped in `ROUTE_NAMES` in `web/src/components/ui/breadcrumb.tsx`

### State Management
- Zustand stores in `web/src/stores/`
- Each store has `loading` state for ProTable integration
- Use `useAuthStore` for auth, `usePermissions` for RBAC

## Common Commands

```bash
# Frontend
cd web && npm run dev        # Start dev server (port 3000)
cd web && npx tsc --noEmit   # Type check
cd web && npm run build      # Production build

# Backend
make run                     # Build and run server
make swagger                 # Regenerate Swagger docs
make contract                # Verify dev contracts
```

## File Structure

```
web/src/
├── app/(dashboard)/     # Page components (React Router)
├── components/          # Shared components
│   ├── ui/             # Reusable UI components
│   └── layout.tsx      # Main layout with sidebar
├── stores/             # Zustand state stores
├── lib/                # Utilities, API helpers
└── App.tsx             # Route definitions

internal/               # Go backend handlers
cmd/core/               # Main server entry
docs/contracts/         # Dev contracts (frontend.md, backend.md)
```

## Do NOT
- Include `/api/v1` in fetch paths (auto-prepended)
- Use antd Button `type="text"` for icon-only buttons (use plain `<button>`)
- Import `DataList` (deprecated, use `ProTable`)
- Add comments unless asked
- Commit secrets or API keys
