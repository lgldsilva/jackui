// JackUI — pipeline CI/CD (Jenkins @ oracle-desktop).
//
// Fluxo:  push na main (webhook Gitea) → test → build → SonarQube (quality gate)
//         → SBOM → Dependency-Track → build imagem → Trivy → push no registry do
//         Gitea. O **Watchtower** no raspberrypi-srv observa o registry e
//         auto-redeploya o container (sem SSH de deploy, sem mudar env).
//
// Pré-requisitos no Jenkins (ver docs/CICD.md):
//   - Plugins: Docker Pipeline, HashiCorp Vault, Gitea.
//   - Agent com /var/run/docker.sock (o controller no oracle-desktop já tem).
//   - Secrets no Vault em `secret/jackui` (KV v2): gitea_user, gitea_token,
//     sonar_token, dt_user, dt_pass.
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
    REGISTRY    = 'gitea.raspberrypi.lan'
    IMAGE       = "gitea.raspberrypi.lan/lgldsilva/jackui"
    TAG         = "${env.GIT_COMMIT?.take(8) ?: env.BUILD_NUMBER}"
    SONAR_HOST  = 'https://sonar.raspberrypi.lan'
    DT_API      = 'https://dependency-track-api.raspberrypi.lan'
    DOCKERFILE  = 'Dockerfile.nvidia'   // variante GPU do deploy padrão
  }

  stages {
    stage('Backend test') {
      agent { docker { image 'golang:1.26-alpine'; reuseNode true } }
      steps {
        sh 'apk add --no-cache gcc musl-dev >/dev/null 2>&1 || true'
        sh 'go test -coverprofile=coverage.out ./internal/... ./cmd/...'
      }
    }

    stage('Frontend build') {
      agent { docker { image 'node:22-alpine'; reuseNode true } }
      steps {
        dir('web') {
          sh 'npm ci'
          sh 'npx tsc --noEmit'
          sh 'npm run build'
        }
      }
    }

    // Quality gate é obrigatório: o estágio QUEBRA o build se o gate falhar
    // (-Dsonar.qualitygate.wait=true). Token vem do Vault.
    stage('SonarQube') {
      agent { docker { image 'sonarsource/sonar-scanner-cli:latest'; reuseNode true } }
      steps {
        withVault(vaultSecrets: [[path: 'secret/jackui', secretValues: [
          [envVar: 'SONAR_TOKEN', vaultKey: 'sonar_token']]]]) {
          sh '''
            sonar-scanner \
              -Dsonar.host.url=$SONAR_HOST \
              -Dsonar.token=$SONAR_TOKEN \
              -Dsonar.projectKey=jackui \
              -Dsonar.sources=. \
              -Dsonar.exclusions='**/node_modules/**,**/dist/**,**/ui/dist/**,**/vendor/**' \
              -Dsonar.go.coverage.reportPaths=coverage.out \
              -Dsonar.tests=. -Dsonar.test.inclusions='**/*_test.go' \
              -Dsonar.coverage.exclusions='web/**' \
              -Dsonar.qualitygate.wait=true
          '''
        }
      }
    }

    stage('SBOM → Dependency-Track') {
      steps {
        withVault(vaultSecrets: [[path: 'secret/jackui', secretValues: [
          [envVar: 'DT_USER', vaultKey: 'dt_user'],
          [envVar: 'DT_PASS', vaultKey: 'dt_pass']]]]) {
          sh '''
            docker run --rm -v "$PWD":/src -w /src ghcr.io/cyclonedx/cdxgen:latest \
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

    // Falha o build se houver CVE CRITICAL na imagem (HIGH só avisa).
    stage('Trivy') {
      steps {
        sh '''
          docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
            aquasec/trivy:latest image --scanners vuln --no-progress \
            --severity HIGH,CRITICAL --exit-code 1 --ignore-unfixed \
            $IMAGE:$TAG
        '''
      }
    }

    // Só publica na main. O push dispara o Watchtower no raspberrypi-srv.
    stage('Push (Gitea registry)') {
      when { branch 'main' }
      steps {
        withVault(vaultSecrets: [[path: 'secret/jackui', secretValues: [
          [envVar: 'GITEA_USER', vaultKey: 'gitea_user'],
          [envVar: 'GITEA_TOKEN', vaultKey: 'gitea_token']]]]) {
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
