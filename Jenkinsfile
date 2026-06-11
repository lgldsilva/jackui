// JackUI — pipeline CI/CD (Jenkins @ oracle-desktop).
//
// Dois modos (multibranch):
//  • PULL REQUEST  → só os GATES: backend test + frontend build/tsc. Se passar,
//    o ci-bot aprova o PR automaticamente (post success). Sem deploy/Sonar/SBOM
//    (SonarQube Community não faz análise de PR; o gate completo roda na main).
//  • main (merge)  → pipeline completo: test → frontend → SonarQube (quality
//    gate) → SBOM→Dependency-Track → build NATIVO amd64 no alvo (SSH) + push no
//    registry do Gitea → Trivy → deploy no raspberrypi-srv → retenção de tags.
//
// Compat: num job single-branch legado (sem BRANCH_NAME) os stages de entrega
// ainda rodam (a condição trata BRANCH_NAME==null como "main"), então a migração
// pro multibranch não derruba o deploy no intervalo.
//
// O Jenkins host (oracle-desktop) é arm64 e o alvo (raspberrypi-srv) é amd64;
// como o alvo é o único consumidor, o build roda LÁ nativamente (sem qemu/OOM).
//
// Pré-requisitos no Jenkins (ver docs/CICD.md):
//   - Plugins: Docker Pipeline, Credentials Binding, Git, SSH Agent, Gitea.
//   - Agent com /var/run/docker.sock (o controller no oracle-desktop já tem).
//   - Credenciais: 'jackui-sonar-token' (secret text), 'jackui-dt' (user/pass),
//     'jackui-gitea' (user/pass, write:package), 'jackui-deploy' (ssh key),
//     'jackui-ci-bot' (secret text — token do ci-bot p/ aprovar PRs).

pipeline {
  agent any

  options {
    timestamps()
    disableConcurrentBuilds()
    timeout(time: 90, unit: 'MINUTES')  // SBOM/cdxgen (~20min) + Sonar são o gargalo
    buildDiscarder(logRotator(numToKeepStr: '20'))
  }

  environment {
    REGISTRY    = '10.228.143.12:3000'
    IMAGE       = "10.228.143.12:3000/lgldsilva/jackui"
    TAG         = "${env.GIT_COMMIT?.take(8) ?: env.BUILD_NUMBER}"
    SONAR_HOST  = 'http://10.228.143.12:9100'
    DT_API      = 'http://10.228.143.12:8081'
    GITEA_API   = 'http://10.228.143.12:3000/api/v1'
    DOCKERFILE  = 'Dockerfile.nvidia'   // variante GPU do deploy padrão
  }

  stages {
    stage('Backend test') {
      // Roda como root p/ instalar ffmpeg (os testes de transcode/streamer o
      // exigem). GOCACHE/GOPATH em /tmp. Só ./internal/... — cmd/server importa o
      // pacote ui (//go:embed all:dist), que não compila antes do frontend build.
      agent { docker { image 'golang:1.26-alpine'; reuseNode true; args '--platform linux/arm64 -u root -e GOCACHE=/tmp/.gocache -e GOPATH=/tmp/.gopath' } }
      steps {
        sh 'apk add --no-cache ffmpeg >/dev/null'
        retry(2) {
          sh 'go test -coverprofile=coverage.out ./internal/...'
        }
        // Streamer tests leave a root-owned runtime dir (internal/streamer/streams,
        // gitignored) that the non-root Sonar scanner can't read (drwx------ root) →
        // AccessDeniedException aborts the analysis. cleanWs (uid 1000) can't delete a
        // root-owned dir either, so it persists and breaks every later build. Remove it
        // here, in the root container that created it, before the scan runs.
        sh 'rm -rf internal/streamer/streams 2>/dev/null || true'
      }
    }

    stage('Frontend build') {
      agent { docker { image 'node:24-alpine'; reuseNode true; args '--platform linux/arm64 -e HOME=/tmp -e npm_config_cache=/tmp/.npm' } }
      steps {
        dir('web') {
          sh 'npm ci'
          sh 'npx tsc --noEmit'
          sh 'npm run build'
        }
      }
    }

    // ───────── A PARTIR DAQUI: só entrega (main / single-branch legado) ─────────

    // Auto-incremento de versão (semver por Conventional Commits). Aqui só
    // CALCULA a próxima vX.Y.Z desde a última tag (feat→minor, fix→patch,
    // !/BREAKING→major) → vira o APP_VERSION do build (aparece no /status). A
    // tag só é CRIADA/publicada depois do deploy OK (stage final), para não
    // deixar tag órfã se o gate ou o deploy falharem.
    stage('Versão (semver)') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        script {
          env.SEMVER = sh(returnStdout: true, script: 'git fetch --tags --quiet || true; bash scripts/semver.sh').trim()
          echo "Versão calculada: ${env.SEMVER}"
        }
      }
    }

    // Quality gate obrigatório: QUEBRA o build se o gate falhar
    // (-Dsonar.qualitygate.wait=true). Token via Jenkins credentials.
    stage('SonarQube') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        withCredentials([string(credentialsId: 'jackui-sonar-token', variable: 'SONAR_TOKEN')]) {
          sh '''
            HOST_WS=$(printf '%s' "$PWD" | sed 's#^/var/jenkins_home#/home/lgldsilva/docker/jenkins/data#')
            docker run --rm --user 0 --platform linux/arm64 -e SONAR_TOKEN -e SONAR_HOST -v "$HOST_WS":/usr/src -w /usr/src \
              eclipse-temurin:21 \
              sh -c '
                echo "Installing Node.js 24..."
                apt-get update -q && apt-get install -y -q ca-certificates curl gnupg unzip wget >/dev/null
                install -m 0755 -d /etc/apt/keyrings
                curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg
                echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_24.x nodistro main" > /etc/apt/sources.list.d/nodesource.list
                apt-get update -q && apt-get install -y -q nodejs >/dev/null
                NODE_BIN=$(command -v node)
                node -v
                if [ ! -d .sonar-scanner ]; then
                  echo "Downloading native arm64 SonarScanner..."
                  wget -q https://binaries.sonarsource.com/Distribution/sonar-scanner-cli/sonar-scanner-cli-8.0.1.6346-linux-aarch64.zip -O /tmp/sonar-scanner.zip
                  unzip -q /tmp/sonar-scanner.zip -d .
                  mv sonar-scanner-8.0.1.6346-linux-aarch64 .sonar-scanner
                  rm -f /tmp/sonar-scanner.zip
                fi
                ./.sonar-scanner/bin/sonar-scanner \
                  -Dsonar.host.url=$SONAR_HOST \
                  -Dsonar.token=$SONAR_TOKEN \
                  -Dsonar.nodejs.executable=$NODE_BIN \
                  -Dsonar.projectKey=jackui \
                  -Dsonar.sources=. \
                  -Dsonar.exclusions="**/node_modules/**,**/dist/**,**/ui/dist/**,**/vendor/**,electron/**,**/streamer/streams/**" \
                  -Dsonar.go.coverage.reportPaths=coverage.out \
                  -Dsonar.tests=. -Dsonar.test.inclusions="**/*_test.go,web/**/*.test.ts,web/**/*.test.tsx,web/**/*.spec.ts,web/**/*.spec.tsx" \
                  -Dsonar.coverage.exclusions="web/**,cmd/**,electron/**" \
                  -Dsonar.scm.disabled=true \
                  -Dsonar.qualitygate.wait=true
              '
          '''
        }
      }
    }

    stage('SBOM → Dependency-Track') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-dt', usernameVariable: 'DT_USER', passwordVariable: 'DT_PASS')]) {
          sh '''
            HOST_WS=$(printf '%s' "$PWD" | sed 's#^/var/jenkins_home#/home/lgldsilva/docker/jenkins/data#')
            rm -rf .cdx-src && mkdir -p .cdx-src
            git archive --format=tar HEAD | tar -x -C .cdx-src
            docker run --rm --user 0 --platform linux/arm64 \
              -v "$HOST_WS/.cdx-src":/src -w /src ghcr.io/cyclonedx/cdxgen:latest \
              --spec-version 1.6 -r -o /src/bom.json . || true
            if [ -s .cdx-src/bom.json ]; then
              JWT=$(curl -sk -X POST "$DT_API/api/v1/user/login" \
                --data-urlencode "username=$DT_USER" --data-urlencode "password=$DT_PASS")
              printf '{"projectName":"jackui","projectVersion":"main","autoCreate":true,"bom":"%s"}' \
                "$(base64 -w0 .cdx-src/bom.json)" > dt-payload.json
              curl -sk -X PUT "$DT_API/api/v1/bom" -H "Authorization: Bearer $JWT" \
                -H 'Content-Type: application/json' --data-binary @dt-payload.json \
                -w '\n[DT upload HTTP %{http_code}]\n'
            else
              echo 'bom.json vazio/ausente — cdxgen falhou; pulando upload pro DT'
            fi
            rm -rf .cdx-src dt-payload.json
          '''
        }
      }
    }

    stage('Build & Push (amd64 nativo no alvo)') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        withCredentials([
          usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GITEA_USER', passwordVariable: 'GITEA_TOKEN'),
          sshUserPrivateKey(credentialsId: 'jackui-deploy', keyFileVariable: 'SSH_KEY', usernameVariable: 'SSH_USER')
        ]) {
          sh '''
            SSH="ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $SSH_USER@10.228.143.1"
            git archive --format=tar HEAD | $SSH "rm -rf /tmp/jackui-build && mkdir -p /tmp/jackui-build && tar -x -C /tmp/jackui-build"
            $SSH "
              set -e
              cd /tmp/jackui-build
              echo '$GITEA_TOKEN' | docker login $REGISTRY -u '$GITEA_USER' --password-stdin
              docker build -f $DOCKERFILE --build-arg BUILD_TIMESTAMP=\\$(date +%s) --build-arg GIT_COMMIT=$GIT_COMMIT --build-arg APP_VERSION=${SEMVER:-$TAG} -t $IMAGE:$TAG -t $IMAGE:nvidia .
              docker push $IMAGE:$TAG
              docker push $IMAGE:nvidia
              docker logout $REGISTRY
              rm -rf /tmp/jackui-build
            "
          '''
        }
      }
    }

    stage('Trivy') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        sh '''
          TRIVY="docker run --rm --platform linux/arm64 -e TRIVY_INSECURE=true aquasec/trivy:latest image --platform linux/amd64 --scanners vuln --no-progress --ignore-unfixed"
          echo "=== Trivy: relatório HIGH+CRITICAL (informativo) ==="
          $TRIVY --severity HIGH,CRITICAL $IMAGE:nvidia || true
          echo "=== Trivy: gate (falha em CRITICAL) ==="
          $TRIVY --severity CRITICAL --exit-code 1 $IMAGE:nvidia
        '''
      }
    }

    stage('Deploy (raspberrypi-srv)') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        withCredentials([sshUserPrivateKey(credentialsId: 'jackui-deploy', keyFileVariable: 'SSH_KEY', usernameVariable: 'SSH_USER')]) {
          sh '''
            ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
              "$SSH_USER"@10.228.143.1 "
                docker pull ${IMAGE}:nvidia &&
                docker tag ${IMAGE}:nvidia jackui:nvidia &&
                docker compose -f /portainer/Files/AppData/Config/jackui/docker-compose.yml up -d --force-recreate jackui &&
                docker image prune -f >/dev/null 2>&1 || true
              "
          '''
        }
      }
    }

    // Tag de versão só DEPOIS do deploy OK (evita tag órfã se algo acima falhar).
    // Idempotente em rebuilds; push best-effort (não derruba um deploy já feito).
    stage('Publicar tag de versão') {
      when {
        allOf {
          anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } }
          expression { return env.SEMVER?.trim() }
        }
      }
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GITEA_USER', passwordVariable: 'GITEA_TOKEN')]) {
          sh '''
            if git rev-parse "refs/tags/$SEMVER" >/dev/null 2>&1; then
              echo "Tag $SEMVER já existe — nada a publicar."
            else
              git tag "$SEMVER"
              if git push "http://$GITEA_USER:$GITEA_TOKEN@10.228.143.12:3000/lgldsilva/jackui.git" "refs/tags/$SEMVER"; then
                echo "Tag $SEMVER publicada no Gitea."
              else
                echo "aviso: push da tag $SEMVER falhou (deploy já concluído; só não registrou a tag)."
              fi
            fi
          '''
        }
      }
    }

    stage('Limpeza de versões antigas') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GU', passwordVariable: 'GT')]) {
          sh '''
            API=http://10.228.143.12:3000/api/v1
            curl -sk -u "$GU:$GT" "$API/packages/lgldsilva?type=container&limit=100" \
              | docker run -i --rm ghcr.io/jqlang/jq:latest -r \
                  '[.[] | select(.name=="jackui" and (.version|test("^[0-9a-f]{8,40}$")))] | sort_by(.created_at) | reverse | .[2:][].version' \
              | while read -r v; do
                  [ -n "$v" ] && curl -sk -u "$GU:$GT" -X DELETE "$API/packages/lgldsilva/container/jackui/$v" -o /dev/null -w "  apagado jackui:$v -> %{http_code}\\n"
                done
            echo "retenção: mantidas :nvidia + 2 últimas tags de git-sha"
          '''
        }
      }
    }
  }

  post {
    always  { sh 'docker image prune -f >/dev/null 2>&1 || true' }
    success {
      script {
        // PR com gates verdes → o ci-bot aprova automaticamente (o Gitea não
        // deixa o autor aprovar o próprio PR; o bot é o "segundo aprovador").
        if (env.CHANGE_ID) {
          withCredentials([string(credentialsId: 'jackui-ci-bot', variable: 'BOTK')]) {
            sh '''
              curl -sf -X POST -H "Authorization: token $BOTK" -H 'Content-Type: application/json' \
                "$GITEA_API/repos/lgldsilva/jackui/pulls/$CHANGE_ID/reviews" \
                -d '{"event":"APPROVED","body":"Gates do CI verdes (backend test + frontend build) — aprovado automaticamente pelo ci-bot."}' \
                -w '\\n[bot approve HTTP %{http_code}]\\n' || echo "aviso: falha ao aprovar via bot (segue sem bloquear)"
            '''
          }
        } else {
          echo "OK — $IMAGE:nvidia publicado e deployado no raspberrypi-srv."
          notifyTelegram("✅ JackUI ${env.SEMVER ?: ''} deployado em produção\nbuild #${env.BUILD_NUMBER} · ${env.GIT_COMMIT?.take(7)}")
        }
      }
    }
    failure {
      script {
        if (!env.CHANGE_ID) {
          notifyTelegram("❌ Build da main do JackUI FALHOU (build #${env.BUILD_NUMBER}) — deploy NÃO saiu.\n${env.BUILD_URL}")
        } else {
          echo 'FALHOU — veja o estágio acima (gate / Trivy / build / deploy).'
        }
      }
    }
  }
}

// Notificação de deploy via Telegram (modelo já usado pelos crons do servidor).
// Credenciais: 'telegram-bot-token' (já existe) + 'telegram-chat-id' (secret
// text com o chat/grupo destino). Sem alguma das duas → skip silencioso, o
// build nunca falha por causa da notificação.
def notifyTelegram(String msg) {
  try {
    withCredentials([string(credentialsId: 'telegram-bot-token', variable: 'TG_TOKEN'),
                     string(credentialsId: 'telegram-chat-id', variable: 'TG_CHAT')]) {
      sh '''
        curl -sf -X POST "https://api.telegram.org/bot$TG_TOKEN/sendMessage" \
          --data-urlencode "chat_id=$TG_CHAT" \
          --data-urlencode "text=''' + msg.replace("'", "") + '''" \
          -o /dev/null -w '[telegram HTTP %{http_code}]\\n' || echo "aviso: notificação Telegram falhou (segue sem bloquear)"
      '''
    }
  } catch (ignored) {
    echo 'telegram: credenciais ausentes — notificação pulada.'
  }
}
