#!/bin/bash
# ──────────────────────────────────────────────────────────────────────────────
# rclone-mount-service.sh — Gerenciador de montagens rclone
# Suporta: Google Drive, OneDrive, iCloud, qualquer remote rclone
# Usa o mesmo padrão do sistema existente (systemd + locks + logs)
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ─── Configurações ────────────────────────────────────────────────────────────
MOUNT_BASE="/mnt/rclone"
LOG_DIR="$HOME/.local/log/rclone"
LOCK_DIR="$HOME/.local/run/rclone-mounts"
CACHE_DIR="$HOME/.cache/rclone"
VFS_CACHE_MAX_SIZE="50G"      # 50GB de cache local por remote
VFS_CACHE_MAX_AGE="168h"      # 7 dias
DIR_CACHE_TIME="1000h"
READ_CHUNK_SIZE="128M"
READ_CHUNK_LIMIT="1G"

# ─── Limpeza de nome para path de montagem ───────────────────────────────────
sanitize() {
  echo "$1" | sed 's/[^a-zA-Z0-9_-]/_/g' | tr '[:upper:]' '[:lower:]'
}

# ─── Logging ─────────────────────────────────────────────────────────────────
mkdir -p "$LOG_DIR" "$LOCK_DIR"
log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_DIR/service.log"; }

# ─── Utilitários ─────────────────────────────────────────────────────────────
check_mount() { mountpoint -q "$1" 2>/dev/null; }

is_gdrive_configured() {
  rclone listremotes 2>/dev/null | grep -qiE "^gdrive|^google"
}

# ─── Determina quais remotes montar ──────────────────────────────────────────
get_gdrive_remotes() {
  rclone listremotes 2>/dev/null | sed 's/://' | while read -r name; do
    local type
    type=$(rclone config dump 2>/dev/null | python3 -c "
import sys, json
try:
    c = json.load(sys.stdin)
    r = c.get('$name', {})
    print(r.get('type', ''))
except: print('')
" 2>/dev/null)
    if [ "$type" = "drive" ]; then
      echo "$name"
    fi
  done
}

get_onedrive_remotes() {
  rclone listremotes 2>/dev/null | sed 's/://' | while read -r name; do
    local type
    type=$(rclone config dump 2>/dev/null | python3 -c "
import sys, json
try:
    c = json.load(sys.stdin)
    r = c.get('$name', {})
    print(r.get('type', ''))
except: print('')
" 2>/dev/null)
    if [ "$type" = "onedrive" ]; then
      echo "$name"
    fi
  done
}

# ─── Monta um remote ─────────────────────────────────────────────────────────
mount_remote() {
  local remote="$1"
  local mount_point="$2"
  local pretty_name="$3"
  local lock_file="$LOCK_DIR/$(sanitize "$remote").lock"

  if check_mount "$mount_point"; then
    log "✓ $pretty_name já montado em $mount_point"
    return 0
  fi

  if [ -f "$lock_file" ]; then
    log "⏳ $pretty_name: montagem em progresso (lock existente)"
    return 1
  fi

  touch "$lock_file"
  trap "rm -f $lock_file" RETURN

  mkdir -p "$mount_point"

  log "📁 Montando $pretty_name ($remote) → $mount_point"

  if rclone mount "$remote:" "$mount_point" \
      --vfs-cache-mode full \
      --allow-other \
      --vfs-read-chunk-size "$READ_CHUNK_SIZE" \
      --vfs-read-chunk-size-limit "$READ_CHUNK_LIMIT" \
      --vfs-cache-max-size "$VFS_CACHE_MAX_SIZE" \
      --vfs-cache-max-age "$VFS_CACHE_MAX_AGE" \
      --dir-cache-time "$DIR_CACHE_TIME" \
      --attr-timeout "$DIR_CACHE_TIME" \
      --vfs-cache-poll-interval 15s \
      --daemon \
      --log-level INFO \
      --log-file "$LOG_DIR/$(sanitize "$remote").log" 2>&1; then
    sleep 3
    if check_mount "$mount_point"; then
      log "✓ $pretty_name montado com sucesso"
      return 0
    else
      log "❌ $pretty_name: montagem OK mas verificação falhou"
      return 1
    fi
  else
    log "❌ $pretty_name: falha ao montar"
    return 1
  fi
}

# ─── Garante permissão do container Docker acessar ────────────────────────────
fix_docker_perms() {
  local mount_point="$1"
  # O container JackUI roda com user/grupo específico, mas allow_other
  # no rclone mount já resolve. Só garantimos permissão de leitura.
  if [ -d "$mount_point" ]; then
    chmod 755 "$mount_point" 2>/dev/null || true
  fi
}

# ─── Gera um systemd mount unit para montagem automática no boot ──────────────
generate_systemd_unit() {
  local remote="$1"
  local mount_point="$2"
  local unit_name
  unit_name="rclone-$(sanitize "$remote").service"

  local service_file="/etc/systemd/system/$unit_name"

  if [ -f "$service_file" ]; then
    log "✓ systemd unit $unit_name já existe"
    return
  fi

  log "📝 Criando systemd unit: $unit_name"
  sudo tee "$service_file" > /dev/null << EOF
[Unit]
Description=rclone mount — $remote
After=network-online.target
Wants=network-online.target
Before=jackui.service

[Service]
Type=forking
User=$USER
Group=$USER
ExecStart=/usr/bin/rclone mount $remote: $mount_point \\
    --vfs-cache-mode full \\
    --allow-other \\
    --vfs-read-chunk-size $READ_CHUNK_SIZE \\
    --vfs-read-chunk-size-limit $READ_CHUNK_LIMIT \\
    --vfs-cache-max-size $VFS_CACHE_MAX_SIZE \\
    --vfs-cache-max-age $VFS_CACHE_MAX_AGE \\
    --dir-cache-time $DIR_CACHE_TIME \\
    --attr-timeout $DIR_CACHE_TIME \\
    --vfs-cache-poll-interval 15s \\
    --log-level INFO \\
    --log-file $LOG_DIR/$(sanitize "$remote").log
ExecStop=/bin/fusermount -u $mount_point
Restart=on-failure
RestartSec=30
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
EOF

  sudo systemctl daemon-reload
  log "✓ systemd unit $unit_name criada. Ative com: sudo systemctl enable --now $unit_name"
}

# ─── Adiciona mounts ao config.yaml do JackUI ─────────────────────────────────
update_jackui_config() {
  local mount_name="$1"
  local mount_point="$2"
  local config_file="${JACKUI_CONFIG:-/portainer/Files/AppData/Config/jackui/config.yaml}"

  if [ ! -f "$config_file" ]; then
    log "⚠ Config $config_file não encontrado — pular update"
    return
  fi

  # Verifica se já tem este mount
  if grep -q "path: $mount_point" "$config_file" 2>/dev/null; then
    log "✓ $mount_name já está no config.yaml"
    return
  fi

  log "📝 Adicionando $mount_name ao config.yaml"
  # Adiciona no bloco external.mounts
  if grep -q "^external:" "$config_file"; then
    if grep -q "^\s\+mounts:" "$config_file"; then
      # Adiciona ao final da lista
      sed -i "/^external:/,\${
        /^\s\+mounts:/,\${
          /^  [a-z]/!{
            /^$/!{
              /- name:/!{
                s/^  mounts:.*/  mounts:\n    - name: $mount_name\n      path: $mount_point/
              }
            }
          }
        }
      }" "$config_file"
    else
      # Cria bloco mounts
      sed -i "/^external:/a\  mounts:\n    - name: $mount_name\n      path: $mount_point" "$config_file"
    fi
  else
    # Cria bloco external inteiro
    echo -e "\nexternal:\n  mounts:\n    - name: $mount_name\n      path: $mount_point" >> "$config_file"
  fi

  log "✓ $mount_name adicionado ao config.yaml"
}

# ─── Main ─────────────────────────────────────────────────────────────────────
log "🚀 Iniciando rclone mount service"
log "   Mount base: $MOUNT_BASE"
log "   Cache VFS:  $VFS_CACHE_MAX_SIZE por remote"
log ""

if ! command -v rclone &>/dev/null; then
  log "❌ rclone não encontrado. Instale com: sudo apt install rclone"
  exit 1
fi

# Descobre remotes automaticamente
GDRIVE_REMOTES=()
while IFS= read -r name; do
  [ -n "$name" ] && GDRIVE_REMOTES+=("$name")
done < <(get_gdrive_remotes)

ONEDRIVE_REMOTES=()
while IFS= read -r name; do
  [ -n "$name" ] && ONEDRIVE_REMOTES+=("$name")
done < <(get_onedrive_remotes)

log "📡 Remotes encontrados:"
log "   Google Drive: ${#GDRIVE_REMOTES[@]} (${GDRIVE_REMOTES[*]:-nenhum})"
log "   OneDrive:     ${#ONEDRIVE_REMOTES[@]} (${ONEDRIVE_REMOTES[*]:-nenhum})"

# Monta Google Drives
FAILED=0
for remote in "${GDRIVE_REMOTES[@]}"; do
  pretty_name="GDrive:$(sanitize "$remote")"
  mount_point="$MOUNT_BASE/gdrive/$(sanitize "$remote")"
  if mount_remote "$remote" "$mount_point" "$pretty_name"; then
    fix_docker_perms "$mount_point"
    update_jackui_config "$pretty_name" "$mount_point" || true
    # Gera systemd unit se não existir
    generate_systemd_unit "$remote" "$mount_point" || true
  else
    ((FAILED++))
  fi
  echo ""
done

# Monta OneDrives (em /mnt/rclone/onedrive/)
for remote in "${ONEDRIVE_REMOTES[@]}"; do
  pretty_name="ODrive:$(sanitize "$remote")"
  mount_point="$MOUNT_BASE/onedrive/$(sanitize "$remote")"

  # Pula se já está montado em /home/lgldsilva/ (legado)
  if check_mount "/home/lgldsilva/OneDrive-$remote" 2>/dev/null; then
    log "⏩ $pretty_name já montado em /home/lgldsilva/ — usando bind mount"
    mount_point="/home/lgldsilva/OneDrive-$remote"
  fi

  if mount_remote "$remote" "$mount_point" "$pretty_name"; then
    fix_docker_perms "$mount_point"
    update_jackui_config "$pretty_name" "$mount_point" || true
    generate_systemd_unit "$remote" "$mount_point" || true
  else
    ((FAILED++))
  fi
  echo ""
done

log ""
log "✅ Serviço finalizado (falhas: $FAILED)"
log ""
log "📋 Próximos passos:"
log "   1. Adicione os bind mounts no docker-compose.yml:"
log "      volumes:"
for remote in "${GDRIVE_REMOTES[@]}"; do
  log "        - $MOUNT_BASE/gdrive/$(sanitize "$remote"):$MOUNT_BASE/gdrive/$(sanitize "$remote")"
done
log ""
log "   2. Reinicie o JackUI: make deploy-auto"
log "   3. Verifique os mounts na página Local do JackUI"
log "   4. Para ativar na inicialização:"
log "      sudo systemctl enable --now rclone-<remote>.service"
