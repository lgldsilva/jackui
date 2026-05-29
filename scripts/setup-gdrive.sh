#!/bin/bash
# ═══════════════════════════════════════════════════════════════════════════════
# setup-gdrive.sh — Configura Google Drives criptografados para o JackUI
#
# Uso:
#   1. Primeira vez — configurar contas manualmente (gera o rclone.conf):
#        ./setup-gdrive.sh configure
#
#   2. Montar tudo e integrar ao JackUI:
#        ./setup-gdrive.sh mount
#
#   3. Ativar na inicialização:
#        ./setup-gdrive.sh install-systemd
#
# ═══════════════════════════════════════════════════════════════════════════════
set -euo pipefail

# ─── Cores ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}ℹ${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
err()   { echo -e "${RED}✗${NC} $*"; }
header(){ echo -e "\n${CYAN}═══ $* ═══${NC}\n"; }

# ─── Configurações ───────────────────────────────────────────────────────────
MOUNT_BASE="/mnt/rclone"
CRYPT_PREFIX="crypt"
LOG_DIR="$HOME/.local/log/rclone"
LOCK_DIR="$HOME/.local/run/rclone-mounts"
VFS_CACHE_MAX_SIZE="50G"
VFS_CACHE_MAX_AGE="168h"

# ─── Utilitários ─────────────────────────────────────────────────────────────
sanitize() { echo "$1" | sed 's/[^a-zA-Z0-9_-]/_/g' | tr '[:upper:]' '[:lower:]'; }
check_mount() { mountpoint -q "$1" 2>/dev/null; }

# ─── Passo 1: Configure ──────────────────────────────────────────────────────
cmd_configure() {
  header "Configuração dos Google Drives + Crypt"

  echo "Este passo vai te guiar na criação de:"
  echo "  1. Remote Google Drive (ex: gdrive1, gdrive2...)"
  echo "  2. Remote Crypt em cima de cada um (ex: gdrive1-crypt)"
  echo ""
  echo "Cada remote crypt criptografa TODOS os arquivos antes de subir pro Google."
  echo "O Google Drive SÓ vê blocos binários sem nome — zero risco."
  echo ""
  read -rp "Quantas contas Google Drive você quer configurar? " QTD
  echo ""

  for i in $(seq 1 "$QTD"); do
    echo ""
    warn "────────── Conta $i de $QTD ──────────"
    read -rp "Nome curto pra conta $i (ex: pessoal, trabalho, familia): " NAME
    NAME=$(sanitize "$NAME")
    REMOTE="gdrive-$NAME"
    CRYPT_REMOTE="$CRYPT_PREFIX-$NAME"

    header "Remote $REMOTE (Google Drive)"
    echo "Criando remote raw do Google Drive..."
    echo 'Siga o wizard do rclone:'
    echo "  - name: $REMOTE"
    echo "  - type: drive"
    echo "  - client_id: deixe em branco (usa o default)"
    echo "  - scope: drive.readonly (só leitura — mais seguro)"
    echo "  - root_folder_id: opcional (pasta específica)"
    echo "  - service_account_file: deixe em branco"
    echo ""
    echo "O rclone vai abrir o browser pra autenticar com o Google."
    echo ""
    read -rp "Pressione Enter pra começar o rclone config... "
    rclone config

    header "Remote $CRYPT_REMOTE (Crypt — criptografado)"
    echo "Criando a camada de criptografia em cima do $REMOTE:"
    echo "  - name: $CRYPT_REMOTE"
    echo "  - type: crypt"
    echo "  - remote: $REMOTE:/jackui-crypt"
    echo "    ⚠ CUIDADO: a pasta 'jackui-crypt' será criada no GDrive"
    echo "  - filename_encryption: standard"
    echo "  - password e password2: use senhas MUITO fortes (64+ chars)"
    echo "    Guarde essas senhas em um cofre (Bitwarden/1Password)!"
    echo "    Se perder, seus arquivos viram pó permanentemente."
    echo ""
    read -rp "Pressione Enter pra criar o crypt remote... "
    rclone config

    ok "$NAME configurado: $REMOTE → $CRYPT_REMOTE"
  done

  header "Resumo dos remotes criados"
  rclone listremotes | sort
  echo ""
  ok "Configure todas as contas! Agora rode: ./setup-gdrive.sh mount"
}

# ─── Passo 2: Mount ──────────────────────────────────────────────────────────
cmd_mount() {
  header "Montando Google Drives Criptografados"

  if ! command -v rclone &>/dev/null; then
    err "rclone não encontrado. Instale: sudo apt install rclone"
    exit 1
  fi

  mkdir -p "$LOG_DIR" "$LOCK_DIR"

  # Descobre remotes crypt
  CRYPT_REMOTES=()
  while IFS= read -r name; do
    name="${name%:}"
    if [[ "$name" == crypt-* ]]; then
      CRYPT_REMOTES+=("$name")
    fi
  done < <(rclone listremotes 2>/dev/null)

  if [ ${#CRYPT_REMOTES[@]} -eq 0 ]; then
    err "Nenhum remote crypt-* encontrado. Primeiro rode: ./setup-gdrive.sh configure"
    exit 1
  fi

  info "Remotes crypt encontrados: ${CRYPT_REMOTES[*]}"
  echo ""

  # Cria pasta de destino se não existir e valida /mnt/rclone
  if [ ! -d "/mnt" ]; then
    warn "/mnt não existe — criando"
    sudo mkdir -p /mnt
  fi

  FAILED=0
  for remote in "${CRYPT_REMOTES[@]}"; do
    # gdrive1-crypt → gdrive1
    local suffix="${remote#crypt-}"
    MOUNT_POINT="$MOUNT_BASE/gdrive/$suffix"
    LOCK_FILE="$LOCK_DIR/$remote.lock"

    if check_mount "$MOUNT_POINT"; then
      ok "$remote já montado em $MOUNT_POINT"
      continue
    fi

    if [ -f "$LOCK_FILE" ]; then
      warn "$remote: montagem em andamento (lock existente)"
      ((FAILED++))
      continue
    fi

    info "Montando $remote → $MOUNT_POINT"
    touch "$LOCK_FILE"

    sudo mkdir -p "$MOUNT_POINT"
    sudo chown "$USER:$USER" "$MOUNT_POINT"

    if rclone mount "$remote:" "$MOUNT_POINT" \
        --vfs-cache-mode full \
        --allow-other \
        --vfs-read-chunk-size 128M \
        --vfs-read-chunk-size-limit 1G \
        --vfs-cache-max-size "$VFS_CACHE_MAX_SIZE" \
        --vfs-cache-max-age "$VFS_CACHE_MAX_AGE" \
        --dir-cache-time 1000h \
        --attr-timeout 1000h \
        --vfs-cache-poll-interval 15s \
        --daemon \
        --log-level INFO \
        --log-file "$LOG_DIR/$remote.log"; then
      sleep 3
      if check_mount "$MOUNT_POINT"; then
        ok "$remote montado"
        rm -f "$LOCK_FILE"
      else
        err "$remote: montou mas verificação falhou"
        ((FAILED++))
      fi
    else
      err "$remote: falha ao montar"
      ((FAILED++))
    fi
  done

  echo ""
  if [ "$FAILED" -eq 0 ]; then
    ok "Todos os ${#CRYPT_REMOTES[@]} remotes montados!"
  else
    warn "$FAILED falha(s). Verifique os logs: $LOG_DIR"
  fi

  # Mostra resumo
  header "Pontos de montagem ativos"
  mount | grep "/mnt/rclone" | awk '{print $1, $3}' || echo "(nenhum)"
  echo ""

  # Gera config pro JackUI
  header "Configuração para o JackUI"
  echo "Adicione no docker-compose.yml, volumes:"
  echo ""
  for remote in "${CRYPT_REMOTES[@]}"; do
    local suffix="${remote#crypt-}"
    echo "      - $MOUNT_BASE/gdrive/$suffix:$MOUNT_BASE/gdrive/$suffix:ro"
  done
  echo ""
  echo "Adicione no JACKUI_EXTERNAL_MOUNTS:"
  echo ""
  local mounts=""
  for remote in "${CRYPT_REMOTES[@]}"; do
    local suffix="${remote#crypt-}"
    if [ -n "$mounts" ]; then mounts+=","; fi
    mounts+="GDrive-$suffix:$MOUNT_BASE/gdrive/$suffix"
  done
  echo "      - JACKUI_EXTERNAL_MOUNTS=$mounts,\${JACKUI_EXTERNAL_MOUNTS}"
  echo ""
  echo "Depois: make deploy-auto"
}

# ─── Passo 3: Systemd ────────────────────────────────────────────────────────
cmd_install_systemd() {
  header "Instalando systemd units para montagem automática"

  mkdir -p "$LOG_DIR" "$LOCK_DIR"

  CRYPT_REMOTES=()
  while IFS= read -r name; do
    name="${name%:}"
    [[ "$name" == crypt-* ]] && CRYPT_REMOTES+=("$name")
  done < <(rclone listremotes 2>/dev/null)

  if [ ${#CRYPT_REMOTES[@]} -eq 0 ]; then
    err "Nenhum remote crypt-* encontrado"
    exit 1
  fi

  for remote in "${CRYPT_REMOTES[@]}"; do
    local suffix="${remote#crypt-}"
    local mount_point="$MOUNT_BASE/gdrive/$suffix"
    local unit_name="rclone-$remote.service"
    local service_file="/etc/systemd/system/$unit_name"

    info "Criando $unit_name"

    sudo tee "$service_file" > /dev/null << EOF
[Unit]
Description=rclone mount — $remote (criptografado)
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
    --vfs-read-chunk-size 128M \\
    --vfs-read-chunk-size-limit 1G \\
    --vfs-cache-max-size $VFS_CACHE_MAX_SIZE \\
    --vfs-cache-max-age $VFS_CACHE_MAX_AGE \\
    --dir-cache-time 1000h \\
    --attr-timeout 1000h \\
    --vfs-cache-poll-interval 15s \\
    --log-level INFO \\
    --log-file $LOG_DIR/$remote.log
ExecStop=/bin/fusermount -u $mount_point
Restart=on-failure
RestartSec=30
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
EOF

    sudo systemctl daemon-reload
    ok "$unit_name criado"
  done

  echo ""
  info "Ative com:"
  for remote in "${CRYPT_REMOTES[@]}"; do
    echo "  sudo systemctl enable --now rclone-$remote.service"
  done
  echo ""
  info "Verifique: systemctl status rclone-crypt-*.service"
}

# ─── Ajuda ────────────────────────────────────────────────────────────────────
cmd_help() {
  echo "Uso: ./setup-gdrive.sh <comando>"
  echo ""
  echo "Comandos:"
  echo "  configure        Cria remotes Google Drive + Crypt (primeira vez)"
  echo "  mount            Monta todos os crypt remotes"
  echo "  install-systemd  Cria systemd units para boot automático"
  echo "  status           Mostra status das montagens"
  echo "  umount           Desmonta todos"
}

cmd_status() {
  header "Status das montagens"
  echo "Remotes rclone:"
  rclone listremotes 2>/dev/null | sort
  echo ""
  echo "Montagens ativas:"
  mount | grep "/mnt/rclone" | awk '{printf "  %s → %s\n", $1, $3}' || echo "  (nenhuma)"
  echo ""
  echo "Systemd units:"
  systemctl list-units --type=mount --all 2>/dev/null | grep "rclone" || echo "  (nenhuma)"
  systemctl list-units --type=service --all 2>/dev/null | grep "rclone" || echo "  (nenhuma)"
}

cmd_umount() {
  header "Desmontando todos os GDrives"
  mount | grep "/mnt/rclone" | awk '{print $3}' | while read -r mp; do
    info "Desmontando $mp"
    fusermount -u "$mp" 2>/dev/null || sudo fusermount -u "$mp" 2>/dev/null || warn "Falha ao desmontar $mp"
  done
  ok "Desmontagem concluída"
}

# ─── Main ─────────────────────────────────────────────────────────────────────
case "${1:-help}" in
  configure)       cmd_configure ;;
  mount)           cmd_mount ;;
  install-systemd) cmd_install_systemd ;;
  status)          cmd_status ;;
  umount)          cmd_umount ;;
  help|--help|-h)  cmd_help ;;
  *)
    err "Comando desconhecido: $1"
    cmd_help
    exit 1
    ;;
esac
