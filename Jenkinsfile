// JackUI — pipeline CI/CD (Jenkins @ oracle-desktop).
//
// Fluxo:  push na main (webhook Gitea) → test → frontend → SonarQube (quality
//         gate) → SBOM→Dependency-Track → build NATIVO amd64 no alvo (SSH) +
//         push no registry do Gitea → Trivy → deploy imediato no raspberrypi-srv
//         (SSH/WireGuard) → retenção das tags antigas.
//
// O Jenkins host (oracle-desktop) é arm64 e o alvo (raspberrypi-srv) é amd64;
// como o alvo é o único consumidor, o build roda LÁ nativamente (sem qemu/OOM).
//
// Pré-requisitos no Jenkins (ver docs/CICD.md):
//   - Plugins: Docker Pipeline, Credentials Binding, Git, SSH Agent.
//   - Agent com /var/run/docker.sock (o controller no oracle-desktop já tem).
//   - Credenciais no Jenkins: 'jackui-sonar-token' (secret text),
//     'jackui-dt' (user/pass), 'jackui-gitea' (user/pass, com write:package),
//     'jackui-deploy' (ssh key — build E deploy no raspberrypi-srv).

pipeline {
  agent any

  options {
    timestamps()
    disableConcurrentBuilds()
    timeout(time: 90, unit: 'MINUTES')  // SBOM/cdxgen (~20min) + Sonar são o gargalo
    buildDiscarder(logRotator(numToKeepStr: '20'))
  }

  environment {
    // Endereços INTERNOS via WireGuard (10.228.143.12 = oracle-desktop) — o
    // container do Jenkins não resolve *.raspberrypi.lan; Gitea/Sonar/DT vivem
    // no mesmo host. Registry em HTTP → exige 10.228.143.12:3000 em
    // insecure-registries do daemon (Jenkins host E raspberrypi-srv p/ o pull).
    REGISTRY    = '10.228.143.12:3000'
    IMAGE       = "10.228.143.12:3000/lgldsilva/jackui"
    TAG         = "${env.GIT_COMMIT?.take(8) ?: env.BUILD_NUMBER}"
    SONAR_HOST  = 'http://10.228.143.12:9100'
    DT_API      = 'http://10.228.143.12:8081'
    DOCKERFILE  = 'Dockerfile.nvidia'   // variante GPU do deploy padrão
  }

  stages {
    stage('Backend test') {
      // Roda como root p/ instalar ffmpeg (os testes de transcode/streamer o
      // exigem, como no ambiente dev). GOCACHE/GOPATH em /tmp. Só ./internal/...
      // — cmd/server importa o pacote ui (//go:embed all:dist), que não compila
      // antes do frontend build; e não tem testes próprios.
      agent { docker { image 'golang:1.26-alpine'; reuseNode true; args '-u root -e GOCACHE=/tmp/.gocache -e GOPATH=/tmp/.gopath' } }
      steps {
        sh 'apk add --no-cache ffmpeg >/dev/null'
        // retry(2): tolera testes flaky de timing (ex: worker startInit, que
        // corre com a goroutine de init). Re-roda a suíte uma vez se falhar.
        retry(2) {
          sh 'go test -coverprofile=coverage.out ./internal/...'
        }
      }
    }

    stage('Frontend build') {
      agent { docker { image 'node:22-alpine'; reuseNode true; args '-e HOME=/tmp -e npm_config_cache=/tmp/.npm' } }
      steps {
        dir('web') {
          sh 'npm ci'
          sh 'npx tsc --noEmit'
          sh 'npm run build'
        }
      }
    }

    // Quality gate obrigatório: o estágio QUEBRA o build se o gate falhar
    // (-Dsonar.qualitygate.wait=true). Token via Jenkins credentials.
    stage('SonarQube') {
      // sonar-scanner-cli não serve como agente (entrypoint roda e sai); roda
      // via `docker run` montando o workspace, igual cdxgen/trivy.
      steps {
        withCredentials([string(credentialsId: 'jackui-sonar-token', variable: 'SONAR_TOKEN')]) {
          sh '''
            docker run --rm --platform linux/amd64 -e SONAR_TOKEN -v "$PWD":/usr/src -w /usr/src \
              sonarsource/sonar-scanner-cli:latest \
              -Dsonar.host.url=$SONAR_HOST \
              -Dsonar.token=$SONAR_TOKEN \
              -Dsonar.projectKey=jackui \
              -Dsonar.sources=. \
              -Dsonar.exclusions='**/node_modules/**,**/dist/**,**/ui/dist/**,**/vendor/**' \
              -Dsonar.go.coverage.reportPaths=coverage.out \
              -Dsonar.tests=. -Dsonar.test.inclusions='**/*_test.go' \
              -Dsonar.coverage.exclusions='web/**,cmd/**' \
              -Dsonar.scm.disabled=true \
              -Dsonar.qualitygate.wait=true
          '''
        }
      }
    }

    stage('SBOM → Dependency-Track') {
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-dt', usernameVariable: 'DT_USER', passwordVariable: 'DT_PASS')]) {
          sh '''
            # cdxgen roda sobre uma ÁRVORE LIMPA (git archive HEAD), não sobre o
            # workspace: o `-r` recursivo no workspace varria web/node_modules
            # (do stage de frontend), .git e artefatos, e o cdxgen:latest (v24,
            # com "SECURE MODE") abortava em ~3s gerando bom vazio. Na árvore
            # limpa ele gera o SBOM normalmente (~300KB). Sem jq no controller →
            # payload via printf/base64 (BOM grande por arquivo, não estoura
            # ARG_MAX). Só sobe se o BOM existir e não for vazio.
            rm -rf .cdx-src && mkdir -p .cdx-src
            git archive --format=tar HEAD | tar -x -C .cdx-src
            docker run --rm --user 0 \
              -v "$PWD/.cdx-src":/src -w /src ghcr.io/cyclonedx/cdxgen:latest \
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

    // Build NATIVO no alvo (raspberrypi-srv, x86_64) via SSH. O Jenkins host é
    // arm64; emular amd64 (qemu) é lento e estourou a memória do host (4.6Gi
    // livres, sem swap) buildando a imagem CUDA. Como o ÚNICO consumidor é o
    // raspberrypi-srv (amd64, 11Gi livres, já com insecure-registry), buildamos
    // lá nativamente: sem qemu, sem OOM, arch exata do deploy. O fonte do commit
    // exato vai por `git archive`→tar via SSH (sem credenciais git no host).
    stage('Build & Push (amd64 nativo no alvo)') {
      steps {
        withCredentials([
          usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GITEA_USER', passwordVariable: 'GITEA_TOKEN'),
          sshUserPrivateKey(credentialsId: 'jackui-deploy', keyFileVariable: 'SSH_KEY', usernameVariable: 'SSH_USER')
        ]) {
          sh '''
            SSH="ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $SSH_USER@10.228.143.1"
            # 1) envia o fonte do commit exato (HEAD do workspace) pro host
            git archive --format=tar HEAD | $SSH "rm -rf /tmp/jackui-build && mkdir -p /tmp/jackui-build && tar -x -C /tmp/jackui-build"
            # 2) builda nativo + push (token/refs expandidos aqui no agente; o host
            #    roda o build). docker login via stdin pra não vazar token no ps.
            $SSH "
              set -e
              cd /tmp/jackui-build
              echo '$GITEA_TOKEN' | docker login $REGISTRY -u '$GITEA_USER' --password-stdin
              docker build -f $DOCKERFILE --build-arg BUILD_TIMESTAMP=\\$(date +%s) -t $IMAGE:$TAG -t $IMAGE:nvidia .
              docker push $IMAGE:$TAG
              docker push $IMAGE:nvidia
              docker logout $REGISTRY
              rm -rf /tmp/jackui-build
            "
          '''
        }
      }
    }

    // Escaneia a variante amd64 (a que roda no deploy-target) direto do registry
    // (TRIVY_INSECURE=true p/ HTTP). Reporta HIGH+CRITICAL; QUEBRA só em CRITICAL.
    stage('Trivy') {
      steps {
        sh '''
          TRIVY="docker run --rm -e TRIVY_INSECURE=true aquasec/trivy:latest image --platform linux/amd64 --scanners vuln --no-progress --ignore-unfixed"
          echo "=== Trivy: relatório HIGH+CRITICAL (informativo) ==="
          $TRIVY --severity HIGH,CRITICAL $IMAGE:nvidia || true
          echo "=== Trivy: gate (falha em CRITICAL) ==="
          $TRIVY --severity CRITICAL --exit-code 1 $IMAGE:nvidia
        '''
      }
    }

    // Deploy imediato no raspberrypi-srv via SSH (WireGuard) — sem esperar o
    // ciclo do Watchtower. Puxa a imagem do registry, re-tag pro nome local que
    // o compose do servidor espera (jackui:nvidia), e recria o container.
    stage('Deploy (raspberrypi-srv)') {
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

    // Retenção: mantém :nvidia (rolling) + as 2 últimas tags :<sha> (atual +
    // anterior, p/ rollback) e apaga as mais antigas no registry (API interna).
    stage('Limpeza de versões antigas') {
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GU', passwordVariable: 'GT')]) {
          sh '''
            API=http://10.228.143.12:3000/api/v1
            # Sem jq no controller → roda jq num container. O filtro considera
            # APENAS tags de git-sha (8-40 hex): ignora :nvidia e os manifests
            # :sha256:... que o Gitea lista como "versões". Mantém as 2 mais
            # recentes (atual + anterior p/ rollback) e apaga as antigas.
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
    success { echo "OK — $IMAGE:nvidia publicado e deployado no raspberrypi-srv." }
    failure { echo 'FALHOU — veja o estágio acima (quality gate / Trivy / build).' }
  }
}
