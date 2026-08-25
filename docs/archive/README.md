# Archive

Historical docs kept for reference. **Not the active source of truth.**

| Path | What it was |
|------|-------------|
| `gitea-workflows/` | Former Gitea Actions workflows (CI, release, mutation, telegram-smoke) before the GitHub migration |
| `gitea-actions-runners.md` | Homelab Gitea runner setup |
| `gitea-scripts/publish-sonar-pr-artifacts.sh` | Publicava `sonar-out/*` como comentário/anexos de PR via **API do Gitea**; morto desde a migração para o GitHub (o SonarCloud comenta o PR sozinho) |
| `CI_CONCURRENCY_PLAN.md` | Plano de concurrency para o **Gitea Actions**; entregue e superado — o `.github/workflows/ci.yml` já tem `concurrency` com `cancel-in-progress` |

Active CI/CD docs: [../CICD.md](../CICD.md)
