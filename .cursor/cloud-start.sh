#!/usr/bin/env bash
# Per-boot Cloud Agent startup. Starts PostgreSQL, ensures the local jackui
# database exists, and writes ~/.jackui/dev.env for `make dev-backend`.
# Exits after readiness; it does not run the app servers.
set -euo pipefail

LOG=/var/log/jackui-cloud-start.log
sudo touch "${LOG}"
sudo chmod 666 "${LOG}"
echo "jackui cloud start $(date -Is)" >>"${LOG}"

NODE_PREFIX=/usr/local/lib/jackui-node
if [[ -x "${NODE_PREFIX}/bin/node" && -d /exec-daemon ]]; then
	sudo ln -sfn "${NODE_PREFIX}/bin/node" /exec-daemon/node
	sudo ln -sfn "${NODE_PREFIX}/bin/npm" /exec-daemon/npm
	sudo ln -sfn "${NODE_PREFIX}/bin/npx" /exec-daemon/npx
fi

PG_VER="$(ls /usr/lib/postgresql | sort -V | tail -1)"
if ! sudo pg_lsclusters --no-header | awk '{print $1,$2}' | grep -qx "${PG_VER} main"; then
	sudo pg_createcluster "${PG_VER}" main
fi

if ! pg_isready -q; then
	# A disk snapshot keeps postmaster.pid and drops the process. If that pid
	# is not alive, remove it so pg_ctlcluster can start instead of refusing.
	data_pid="/var/lib/postgresql/${PG_VER}/main/postmaster.pid"
	if [[ -f "${data_pid}" ]]; then
		old_pid="$(sudo awk 'NR==1 { print; exit }' "${data_pid}" || true)"
		if [[ -z "${old_pid}" ]] || ! sudo kill -0 "${old_pid}" 2>/dev/null; then
			echo "removing stale postmaster.pid (${old_pid:-empty})" >>"${LOG}"
			sudo rm -f "${data_pid}" "/var/run/postgresql/${PG_VER}-main.pid"
		fi
	fi
	sudo pg_ctlcluster "${PG_VER}" main start || sudo pg_ctlcluster "${PG_VER}" main start
fi
echo "postgres ready" >>"${LOG}"

ready=0
for _ in $(seq 1 30); do
	if pg_isready -q; then
		ready=1
		break
	fi
	sleep 1
done
if [[ "${ready}" -ne 1 ]]; then
	echo "PostgreSQL did not become ready" >&2
	exit 1
fi

if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='ubuntu'" | grep -qx 1; then
	sudo -u postgres createuser ubuntu --createdb
fi
sudo -u postgres psql -c "ALTER ROLE ubuntu WITH LOGIN CREATEDB;" >/dev/null
if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='jackui'" | grep -qx 1; then
	sudo -u postgres createdb --owner=ubuntu jackui
fi

mkdir -p "${HOME}/.jackui/streams" "${HOME}/.jackui/downloads" "${HOME}/.jackui/library"
# Quote every value. The DSN contains "&", which background-jobs an unquoted assignment.
cat >"${HOME}/.jackui/dev.env" <<EOF
JACKUI_DATABASE_URL='postgres://ubuntu@/jackui?host=/var/run/postgresql&sslmode=disable'
JACKUI_AUTH_ENABLED=0
JACKUI_ALLOW_INSECURE_AUTH=1
JACKUI_STREAM_DIR='${HOME}/.jackui/streams'
JACKUI_DOWNLOAD_DIR='${HOME}/.jackui/downloads'
JACKUI_SHARED_DIR='${HOME}/.jackui/library'
JACKUI_EXTERNAL_MOUNTS='Library:${HOME}/.jackui/library'
TZ=America/Sao_Paulo
EOF

if [[ -f config.yaml.example && ! -f config.yaml ]]; then
	cp config.yaml.example config.yaml
fi
