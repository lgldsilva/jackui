// JackUI — pipeline CI/CD (Jenkins @ oracle-desktop).
//
// Dois modos (multibranch):
//  • PULL REQUEST  → só os GATES: backend test + frontend tsc/test/build. Se passar,
//    o ci-bot aprova o PR automaticamente (post success). Sem deploy/Sonar/SBOM
//    (SonarQube Community não faz análise de PR; o gate completo roda na main).
//  • main (merge)  → pipeline completo: test → frontend → SonarQube (quality gate)
//    → EM PARALELO { SBOM→Dependency-Track no ARM (nativo, offload do homeserver) ||
//    build amd64 local + push no Gitea → Trivy → deploy } → publica tag + retenção.
//    O SBOM/cdxgen (~lento) saiu do caminho crítico: roda no ARM sobrepondo o build.
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
  // Em PR (CHANGE_ID setado) o build roda no AGENTE ARM (oci-ampere-1, offload de CPU); na main
  // continua no controller built-in (intocado — deploy depende da GPU/docker.sock locais).
  agent { label env.CHANGE_ID ? 'arm64' : 'built-in' }

  options {
    timestamps()
    disableConcurrentBuilds()
    timeout(time: 90, unit: 'MINUTES')  // SBOM/cdxgen (~20min) + Sonar são o gargalo
    buildDiscarder(logRotator(numToKeepStr: '20'))
    // O checkout do PRÓXIMO build roda como jenkins (uid 1000) e limpa o workspace
    // antes de clonar; se sobrar QUALQUER arquivo root (de um stage docker --user 0),
    // o `git clean` falha com "Operation not permitted" e o clone aborta. Por isso
    // desligamos o checkout automático e fazemos a limpeza+checkout NÓS, num stage
    // inicial controlado (ver stage 'Limpeza + Checkout' abaixo). Causa raiz é
    // atacada nos stages docker (chown de volta p/ uid 1000 + .scannerwork em /tmp);
    // este skip é o cinto-e-suspensório p/ restos não-root.
    skipDefaultCheckout(true)
  }

  environment {
    REGISTRY    = '192.168.0.100:3000'
    IMAGE       = "192.168.0.100:3000/lgldsilva/jackui"
    // TAG (git-sha8) é definido no stage 'Limpeza + Checkout', DEPOIS do
    // checkout — com skipDefaultCheckout(true) o env.GIT_COMMIT ainda é null
    // aqui no startup, e cair no BUILD_NUMBER geraria tag numérica que o regex
    // de retenção (^[0-9a-f]{8,40}$) não casa → tags vazariam pra sempre.
    SONAR_HOST  = 'http://10.228.143.12:9100'
    DT_API      = 'http://10.228.143.12:8081'
    // cdxgen PINADO por digest (imutável): a tag :latest do cdxgen segue o master e
    // re-puxaria a cada build. Este é o manifesto arm64, pré-cacheado nos DOIS nós ARM
    // -> o stage SBOM não paga pull. Atualizar: puxar :latest num nó ARM, pegar o novo
    // RepoDigest (docker inspect --format '{{index .RepoDigests 0}}') e trocar aqui.
    CDXGEN_IMAGE = 'ghcr.io/cyclonedx/cdxgen@sha256:d3c4515fd3624488039cf62bbeb806532beabe85e099dd1ea09a72d5f722b7bd'
    GITEA_API   = 'https://gitea.raspberrypi.lan/api/v1'   // hostname via NPM/CA: alcançável do controller E do agente ARM (o ci-bot approve roda no nó do build)
    DOCKERFILE  = 'Dockerfile.nvidia'   // variante GPU do deploy padrão
  }

  stages {
    // Com skipDefaultCheckout(true), fazemos o checkout aqui — DEPOIS de varrer
    // restos de builds anteriores. O `rm` roda como jenkins (uid 1000), então só
    // apaga o que NÃO for root; os stages docker abaixo garantem (via chown) que
    // nada root sobre. Mesmo assim varremos aqui p/ cobrir artefatos não-root
    // (.scannerwork agora vai p/ /tmp, mas mantemos na lista por segurança).
    stage('Limpeza + Checkout') {
      steps {
        sh 'rm -rf .scannerwork .sonar-scanner .cdx-src bom.json dt-payload.json coverage.out internal/streamer/streams 2>/dev/null || true'
        // CAPTURA o retorno do checkout: com skipDefaultCheckout(true), o
        // `checkout scm` sozinho NÃO exporta env.GIT_COMMIT pro resto do
        // pipeline (só o checkout automático fazia). Sem isto, GIT_COMMIT fica
        // vazio → build-arg vazio (/status sem commit) e TAG cai no BUILD_NUMBER
        // numérico, que o regex de retenção (^[0-9a-f]{8,40}$) não casa → tags
        // vazariam no registry. Pegamos o sha do mapa de retorno e exportamos.
        script {
          def scmVars = checkout scm
          env.GIT_COMMIT = scmVars.GIT_COMMIT
          env.TAG = env.GIT_COMMIT?.take(8) ?: env.BUILD_NUMBER
        }
        // PostgreSQL sidecar for the backend tests (the stores now run on
        // Postgres). On a dedicated docker network so the test container reaches
        // it by name. Tuned for throwaway speed (fsync off). The test container
        // joins this network and gets JACKUI_TEST_DATABASE_URL (see Backend test).
        sh '''
          docker network create jackui-ci-net >/dev/null 2>&1 || true
          docker rm -f jackui-ci-pg >/dev/null 2>&1 || true
          docker run -d --name jackui-ci-pg --network jackui-ci-net \
            -e POSTGRES_USER=jackui -e POSTGRES_PASSWORD=ci -e POSTGRES_DB=jackui \
            postgres:16-alpine \
            -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=300 >/dev/null
          for i in $(seq 1 30); do docker exec jackui-ci-pg pg_isready -U jackui -d jackui >/dev/null 2>&1 && break; sleep 1; done
        '''
      }
    }

    stage('Backend test') {
      // Roda como root p/ instalar ffmpeg (os testes de transcode/streamer o
      // exigem). GOCACHE/GOPATH em /tmp. Só ./internal/... — cmd/server importa o
      // pacote ui (//go:embed all:dist), que não compila antes do frontend build.
      // PR roda no ARM -> nativo arm64 (sem --platform, senão QEMU); main força amd64 (casa com o deploy GPU).
      agent { docker { image 'golang:1.26-alpine'; reuseNode true; args "${env.CHANGE_ID ? '' : '--platform linux/amd64'} -u root --network jackui-ci-net -e GOCACHE=/tmp/.gocache -e GOPATH=/tmp/.gopath -e JACKUI_TEST_DATABASE_URL=postgres://jackui:ci@jackui-ci-pg:5432/jackui?sslmode=disable" } }
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
        // CAUSA RAIZ do "Operation not permitted" no checkout: este container roda
        // como root e o coverage.out (e qualquer outro artefato) nasce dono=root no
        // bind-mount do workspace. O checkout do PRÓXIMO build (uid 1000) não
        // consegue limpá-lo → clone aborta. Devolvemos a posse ao jenkins (uid 1000)
        // AINDA dentro do container root, que é o único que consegue fazer o chown.
        // O Sonar (que roda --user 0) lê coverage.out sem problema mesmo assim.
        sh 'chown -R 1000:1000 . 2>/dev/null || true'
      }
    }

    stage('Frontend build') {
      agent { docker { image 'node:24-alpine'; reuseNode true; args "${env.CHANGE_ID ? '' : '--platform linux/amd64'} -e HOME=/tmp -e npm_config_cache=/tmp/.npm" } }
      steps {
        dir('web') {
          sh 'npm ci'
          sh 'npx tsc --noEmit'
          sh 'npm test'          // vitest run — pega regressões de funções puras (group, parser, etc.)
          sh 'npm run build'
        }
        // node:alpine roda como root por padrão: web/node_modules e web/dist nascem
        // dono=root no workspace e quebrariam o checkout do próximo build (uid 1000).
        // Devolvemos a posse ao jenkins aqui, dentro do próprio container.
        sh 'chown -R 1000:1000 . 2>/dev/null || true'
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
        withCredentials([string(credentialsId: 'jackui-sonar-token-arm', variable: 'SONAR_TOKEN')]) {
          sh '''
            HOST_WS=$(printf '%s' "$PWD" | sed 's#^/var/jenkins_home#/storage/dev/jenkins/data#')
            docker run --rm --user 0 --platform linux/amd64 -e SONAR_TOKEN -e SONAR_HOST -v "$HOST_WS":/usr/src -w /usr/src \
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
                  echo "Downloading native amd64 SonarScanner..."
                  wget -q https://binaries.sonarsource.com/Distribution/sonar-scanner-cli/sonar-scanner-cli-8.0.1.6346-linux-x64.zip -O /tmp/sonar-scanner.zip
                  unzip -q /tmp/sonar-scanner.zip -d .
                  mv sonar-scanner-8.0.1.6346-linux-x64 .sonar-scanner
                  rm -f /tmp/sonar-scanner.zip
                fi
                # .scannerwork FORA do workspace: o scanner cria esse dir no CWD e,
                # como rodamos --user 0, ele nasceria dono=root no workspace e
                # quebraria o checkout do próximo build. Em /tmp ele some com o
                # container e nunca toca o workspace.
                ret=0
                ./.sonar-scanner/bin/sonar-scanner \
                  -Dsonar.host.url=$SONAR_HOST \
                  -Dsonar.token=$SONAR_TOKEN \
                  -Dsonar.nodejs.executable=$NODE_BIN \
                  -Dsonar.working.directory=/tmp/.scannerwork \
                  -Dsonar.projectKey=jackui \
                  -Dsonar.sources=. \
                  -Dsonar.exclusions="**/node_modules/**,**/dist/**,**/ui/dist/**,**/vendor/**,electron/**,**/streamer/streams/**" \
                  -Dsonar.go.coverage.reportPaths=coverage.out \
                  -Dsonar.tests=. -Dsonar.test.inclusions="**/*_test.go,web/**/*.test.ts,web/**/*.test.tsx,web/**/*.spec.ts,web/**/*.spec.tsx" \
                  -Dsonar.coverage.exclusions="web/**,cmd/**,electron/**" \
                   -Dsonar.scm.disabled=true || ret=$?
                # Causa raiz: o .sonar-scanner (cache, mantido entre builds de
                # propósito) e o coverage.out ficam dono=root no workspace montado.
                # Devolvemos a posse ao jenkins (uid 1000) AQUI, dentro do container
                # root — única forma de o checkout seguinte conseguir limpá-los.
                # Feito mesmo se o gate falhar (não mascara o exit: propagamos $ret).
                chown -R 1000:1000 /usr/src 2>/dev/null || true
                exit $ret
              '
          '''
        }
      }
    }

    // Depois do gate (Sonar), a ENTREGA (build/scan/deploy, no built-in com GPU +
    // docker.sock locais) roda EM PARALELO com o SBOM (que foi pro ARM). Antes o
    // SBOM/cdxgen (~20min) ficava no caminho crítico ANTES do build; agora sai do
    // homeserver (offload pro ARM ocioso) e SOBREPÕE o build em vez de somar. O SBOM
    // não é gate (cdxgen roda com `|| true`; upload pro DT não derruba o build), então
    // paralelizar e jogar num nó separado é seguro. Os `when` migraram pro stage-pai;
    // os filhos herdam. O Dockerfile.nvidia é self-contained (builda front+back do
    // source), então a Entrega só precisa do checkout — não de artefato dos gates.
    stage('Entrega + SBOM (paralelo)') {
      when { anyOf { branch 'main'; expression { return env.BRANCH_NAME == null } } }
      parallel {
        // ───── SBOM no ARM (nativo arm64), FORA do caminho crítico ─────
        stage('SBOM → Dependency-Track (arm64)') {
          agent { label 'arm64' }
          steps {
            // Workspace próprio no agente ARM (skipDefaultCheckout é global) — este
            // ramo roda em paralelo com a entrega no built-in, então faz o SEU checkout,
            // só pro `git archive`. rm defensivo antes: restos root de um cdxgen anterior
            // são chownados de volta no fim, mas varremos por segurança.
            sh 'rm -rf .cdx-src bom.json dt-payload.json 2>/dev/null || true'
            // NÃO usar `checkout scm`: o job single-branch 'jackui' aponta o SCM pro IP de
            // LAN 192.168.0.100:3000, INALCANÇÁVEL dos nós ARM (cloud, via WireGuard). Checa
            // pelo hostname gitea.raspberrypi.lan, que o agente ARM resolve pro NPM via
            // extra_hosts (cert da CA interna confiado pela imagem) — mesmo caminho que os
            // builds de PR já usam no ARM. Fixa o SHA exato do build (env.GIT_COMMIT, setado
            // no 'Limpeza + Checkout' do built-in) pra o SBOM casar com o que foi deployado.
            checkout([$class: 'GitSCM',
              branches: [[name: env.GIT_COMMIT]],
              userRemoteConfigs: [[url: 'https://gitea.raspberrypi.lan/lgldsilva/jackui.git',
                                   credentialsId: 'jackui-gitea']]
            ])
            withCredentials([usernamePassword(credentialsId: 'jackui-dt-arm', usernameVariable: 'DT_USER', passwordVariable: 'DT_PASS')]) {
              sh '''
                # No agente ARM o workspace mapeia /home/jenkins/agent -> /storage/dev/jenkins-agent
                # no host (bind do serviço swarm). O `docker run -v` fala com o docker.sock do
                # ARM, então a ORIGEM do volume tem que ser o path do HOST, não o do container.
                HOST_WS=$(printf '%s' "$PWD" | sed 's#^/home/jenkins/agent#/storage/dev/jenkins-agent#')
                rm -rf .cdx-src && mkdir -p .cdx-src
                git archive --format=tar HEAD | tar -x -C .cdx-src
                # cdxgen NATIVO arm64 (sem --platform -> sem QEMU). FETCH_LICENSE=false corta a
                # resolução de licenças pela rede (o maior gargalo do cdxgen); -t go/javascript
                # escopa a análise aos 2 ecossistemas do repo em vez de sondar tudo. Imagem
                # PINADA por digest (env CDXGEN_IMAGE), pré-cacheada nos nós ARM -> zero re-pull.
                # cdxgen roda --user 0 -> bom.json/.cdx-src nascem dono=root; o entrypoint chowna
                # de volta p/ 1000 antes de sair, senão o checkout do próximo build não limpa.
                docker run --rm --user 0 -e FETCH_LICENSE=false --entrypoint sh \
                  -v "$HOST_WS/.cdx-src":/src -w /src "$CDXGEN_IMAGE" \
                  -c 'cdxgen --spec-version 1.6 -r -t go -t javascript -o /src/bom.json . ; chown -R 1000:1000 /src 2>/dev/null || true' || true
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

        // ───── ENTREGA no built-in (GPU + docker.sock locais): build → scan → deploy ─────
        stage('Entrega') {
          stages {
            stage('Build & Push (amd64 nativo, local)') {
              steps {
                // Jenkins roda NO hub amd64 com docker.sock montado: build/push direto,
                // sem SSH (antes fazia ssh ao alvo quando o Jenkins era remoto no Oracle ARM).
                withCredentials([
                  usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GITEA_USER', passwordVariable: 'GITEA_TOKEN')
                ]) {
                  sh '''
                    set -e
                    echo "$GITEA_TOKEN" | docker login $REGISTRY -u "$GITEA_USER" --password-stdin
                    docker build -f $DOCKERFILE \
                      --build-arg BUILD_TIMESTAMP=$(date +%s) \
                      --build-arg GIT_COMMIT=$GIT_COMMIT \
                      --build-arg APP_VERSION=${SEMVER:-$TAG} \
                      -t $IMAGE:$TAG -t $IMAGE:nvidia .
                    docker push $IMAGE:$TAG
                    docker push $IMAGE:nvidia
                    docker logout $REGISTRY
                  '''
                }
              }
            }

            stage('Trivy') {
              steps {
                sh '''
                  TRIVY="docker run --rm --platform linux/amd64 -e TRIVY_INSECURE=true aquasec/trivy:latest image --platform linux/amd64 --scanners vuln --no-progress --ignore-unfixed"
                  echo "=== Trivy: relatório HIGH+CRITICAL (informativo) ==="
                  $TRIVY --severity HIGH,CRITICAL $IMAGE:nvidia || true
                  echo "=== Trivy: gate (falha em CRITICAL) ==="
                  $TRIVY --severity CRITICAL --exit-code 1 $IMAGE:nvidia
                '''
              }
            }

            stage('Deploy (local)') {
              steps {
                // Deploy local via docker.sock (sem SSH): pull, retag e sobe o compose no host.
                sh '''
                  set -e
                  docker pull ${IMAGE}:nvidia
                  docker tag ${IMAGE}:nvidia jackui:nvidia
                  docker compose -f /portainer/Files/AppData/Config/jackui/docker-compose.yml up -d --force-recreate jackui
                  docker image prune -f >/dev/null 2>&1 || true
                '''
              }
            }
          }
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
              if git push "http://$GITEA_USER:$GITEA_TOKEN@192.168.0.100:3000/lgldsilva/jackui.git" "refs/tags/$SEMVER"; then
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
            API=http://192.168.0.100:3000/api/v1
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
    always  {
      sh 'docker rm -f jackui-ci-pg >/dev/null 2>&1 || true; docker network rm jackui-ci-net >/dev/null 2>&1 || true'
      sh 'docker image prune -f >/dev/null 2>&1 || true'
    }
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
