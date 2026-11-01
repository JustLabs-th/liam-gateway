#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p .data/archives
if [ ! -f .env ]; then
  cp .env.example .env
  GATEWAY_SETUP_KEY=$(openssl rand -base64 32)
  GATEWAY_SETUP_KEY="$GATEWAY_SETUP_KEY" python3 - <<'PYCODE'
import os
from pathlib import Path
p = Path('.env')
p.write_text(p.read_text().replace('GATEWAY_ENCRYPTION_KEY=\n', 'GATEWAY_ENCRYPTION_KEY=' + os.environ['GATEWAY_SETUP_KEY'] + '\n'))
p.chmod(0o600)
PYCODE
fi
if [ ! -f configs/local.yaml ]; then cp configs/example.yaml configs/local.yaml; fi
printf '%s\n' 'Local config prepared. Configure provider model IDs and prices in Admin UI after startup.'
