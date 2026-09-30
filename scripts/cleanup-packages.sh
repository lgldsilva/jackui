#!/bin/bash
# Cleanup of old container packages in the Gitea Registry.
# Keeps the :nvidia tag and the 2 most recent commit-sha versions.

set -e

# GITEA_API/GITEA_USER come from the workflow (github.api_url / github.repository_owner);
# no hardcoded default so the internal host does not leak into the repository.
GITEA_API="${GITEA_API}"
GITEA_USER="${GITEA_USER}"
GITEA_TOKEN="${GITEA_TOKEN}"

if [ -z "$GITEA_TOKEN" ] || [ -z "$GITEA_API" ] || [ -z "$GITEA_USER" ]; then
  echo "Warning: GITEA_API/GITEA_USER/GITEA_TOKEN missing. Skipping package cleanup."
  exit 0
fi

# Verified TLS (mirrors scripts/publish-release.sh): when the runner bakes the
# internal Gitea CA, pin every call to it; otherwise fall back to the OS trust
# store (which already resolves Gitea). Overridable via GITEA_CA. Verification
# is NEVER disabled on these token-bearing calls.
GITEA_CA="${GITEA_CA:-/usr/local/share/ca-certificates/gitea-ca.crt}"
CURL_CA=()
[ -f "$GITEA_CA" ] && CURL_CA=(--cacert "$GITEA_CA")
if [ -f "$GITEA_CA" ]; then
  # Export for the python block below (urllib uses the same bundle).
  export GITEA_CA
else
  unset GITEA_CA
fi

echo "=== Starting cleanup of old jackui packages ==="

# Fetches the package list via the API and filters it using jq.
# If the runner has jq installed (the catthehacker/ubuntu image ships jq natively),
# we do not need to run jq through a Docker container!
# We test whether jq is available; otherwise we use python, which is always available.
# Using python3 to process the JSON is more portable and guaranteed in any working container.

python3 -c '
import sys, json, os, urllib.request, ssl

# Verified TLS: pin the runner-baked Gitea CA when provided (GITEA_CA),
# otherwise use the system trust store. Never CERT_NONE on a token-bearing call.
ca_file = os.environ.get("GITEA_CA", "")
if ca_file and os.path.isfile(ca_file):
    ctx = ssl.create_default_context(cafile=ca_file)
else:
    ctx = ssl.create_default_context()

req = urllib.request.Request(
    "'"$GITEA_API"'/packages/'"$GITEA_USER"'?type=container&limit=100",
    headers={"Authorization": "token '"$GITEA_TOKEN"'"}
)

try:
    with urllib.request.urlopen(req, context=ctx) as response:
        packages = json.loads(response.read().decode())
        
    # Filters packages named jackui whose version is a commit hash (sha8 through sha40)
    import re
    commit_regex = re.compile(r"^[0-9a-f]{8,40}$")
    
    jackui_versions = [
        p for p in packages 
        if p.get("name") == "jackui" and commit_regex.match(p.get("version", ""))
    ]
    
    # Sorts by creation date (newest first)
    jackui_versions.sort(key=lambda x: x.get("created_at", ""), reverse=True)
    
    # Keeps the 2 newest, deletes the rest
    to_delete = jackui_versions[2:]
    
    for p in to_delete:
        version = p["version"]
        print(version)
except Exception as e:
    print(f"Error fetching/filtering packages: {e}", file=sys.stderr)
    sys.exit(1)
' | while read -r v; do
  if [ -n "$v" ]; then
    echo "Deleting package jackui:$v..."
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
      "${CURL_CA[@]+"${CURL_CA[@]}"}" \
      -H "Authorization: token $GITEA_TOKEN" \
      "$GITEA_API/packages/$GITEA_USER/container/jackui/$v")
    echo "  -> Result: HTTP $HTTP_CODE"
  fi
done

echo "=== Package cleanup completed ==="
