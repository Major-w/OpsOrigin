#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

usage() {
  echo "Usage: $0 [up|down|restart|logs|status]"
  echo ""
  echo "Commands:"
  echo "  up       Start all services (docker compose up -d)"
  echo "  down     Stop and remove all services"
  echo "  restart  Restart all services"
  echo "  logs     Follow logs from all services"
  echo "  status   Show running containers"
  exit 1
}

CMD="${1:-}"
if [[ -z "$CMD" ]]; then
  usage
fi

cd "$PROJECT_DIR"

case "$CMD" in
  up)
    echo "==> Starting services..."
    docker compose up -d --build
    echo "==> Services started."
    docker compose ps
    ;;
  down)
    echo "==> Stopping services..."
    docker compose down
    echo "==> Services stopped."
    ;;
  restart)
    echo "==> Restarting services..."
    docker compose restart
    echo "==> Services restarted."
    ;;
  logs)
    docker compose logs -f
    ;;
  status)
    docker compose ps
    ;;
  *)
    usage
    ;;
esac
