// JackUI — pipeline CI/CD (Jenkins @ oracle-desktop).
//
// Fluxo:  push na main (webhook Gitea) → test → build → SonarQube (quality gate)
//         → SBOM → Dependency-Track → build imagem → Trivy → push no registry do
//         Gitea. O **Watchtower** no raspberrypi-srv observa o registry e
//         auto-redeploya o container (sem SSH de deploy, sem mudar env).
//
// Pré-requisitos no Jenkins (ver docs/CICD.md):
//   - Plugins: Docker Pipeline, Credentials Binding, Git.
//   - Agent com /var/run/docker.sock (o controller no oracle-desktop já tem).
//   - Credenciais no Jenkins: 'jackui-sonar-token' (secret text),
//     'jackui-dt' (user/pass), 'jackui-gitea' (user/pass, com write:package).
//   - Watchtower no raspberrypi-srv observando a imagem do registry (label
//     com.centurylinklabs.watchtower.enable=true no compose do jackui).

pipeline {
  agent any

  options {
    timestamps()
    disableConcurrentBuilds()
    timeout(time: 40, unit: 'MINUTES')
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
        sh 'go test -coverprofile=coverage.out ./internal/...'
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
            docker run --rm --user 0 -v "$PWD":/src -w /src ghcr.io/cyclonedx/cdxgen:latest \
              --spec-version 1.6 -r -o bom.json . || true
            JWT=$(curl -sk -X POST "$DT_API/api/v1/user/login" \
              --data-urlencode "username=$DT_USER" --data-urlencode "password=$DT_PASS")
            curl -sk -X PUT "$DT_API/api/v1/bom" -H "Authorization: Bearer $JWT" \
              -H 'Content-Type: application/json' \
              -d "$(jq -n --arg b "$(base64 -w0 bom.json)" \
                   '{projectName:"jackui",projectVersion:"main",autoCreate:true,bom:$b}')"
          '''
        }
      }
    }

    stage('Build imagem') {
      steps {
        sh 'docker build --build-arg BUILD_TIMESTAMP=$(date +%s) -f $DOCKERFILE -t $IMAGE:$TAG -t $IMAGE:nvidia .'
      }
    }

    // Reporta HIGH+CRITICAL (visível, não bloqueia — HIGH de base-image é
    // aceitável/documentado), e QUEBRA o build só se houver CVE CRITICAL.
    stage('Trivy') {
      steps {
        sh '''
          TRIVY="docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --scanners vuln --no-progress --ignore-unfixed"
          echo "=== Trivy: relatório HIGH+CRITICAL (informativo) ==="
          $TRIVY --severity HIGH,CRITICAL $IMAGE:$TAG || true
          echo "=== Trivy: gate (falha em CRITICAL) ==="
          $TRIVY --severity CRITICAL --exit-code 1 $IMAGE:$TAG
        '''
      }
    }

    // Só publica na main. O push dispara o Watchtower no raspberrypi-srv.
    stage('Push (Gitea registry)') {
      when { branch 'main' }
      steps {
        withCredentials([usernamePassword(credentialsId: 'jackui-gitea', usernameVariable: 'GITEA_USER', passwordVariable: 'GITEA_TOKEN')]) {
          sh '''
            echo "$GITEA_TOKEN" | docker login $REGISTRY -u "$GITEA_USER" --password-stdin
            docker push $IMAGE:$TAG
            docker push $IMAGE:nvidia
            docker logout $REGISTRY
          '''
        }
      }
    }
  }

  post {
    always  { sh 'docker image prune -f >/dev/null 2>&1 || true' }
    success { echo "OK — $IMAGE:nvidia publicado; Watchtower fará o rollout no raspberrypi-srv." }
    failure { echo 'FALHOU — veja o estágio acima (quality gate / Trivy / build).' }
  }
}
