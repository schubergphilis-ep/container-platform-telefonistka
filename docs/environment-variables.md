# Environment Variables

## Provider Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `GITHUB_OAUTH_TOKEN` | GitHub only | — | GitHub personal access token |
| `GITHUB_APP_ID` | GitHub App | — | GitHub App ID (alternative to OAuth) |
| `GITHUB_APP_PRIVATE_KEY_PATH` | GitHub App | — | Path to GitHub App private key PEM file |
| `GITHUB_WEBHOOK_SECRET` | Recommended | — | Secret for validating GitHub webhook signatures |
| `GITHUB_HOST` | No | `github.com` | GitHub Enterprise hostname |
| `GITLAB_TOKEN` | GitLab only | — | GitLab API access token |
| `GITLAB_URL` | No | `https://gitlab.com` | GitLab instance URL |
| `GITLAB_WEBHOOK_SECRET` | Recommended | — | Secret for validating GitLab webhook signatures |
| `GITLAB_APPROVER_TOKEN` | No | — | Separate token for auto-approving MRs |
| `GITLAB_PROJECT_ID` | No | Auto-detected | GitLab project ID (numeric or `group/project`) |
| `GITLAB_INSECURE` | No | `false` | Skip TLS verification for self-hosted GitLab |

## Server Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `ALLOW_UNSIGNED_WEBHOOKS` | No | `false` | Accept webhooks without signature validation (**not recommended**) |
| `LOG_LEVEL` | No | `info` | Log level: `debug`, `info`, `warn`, `error` |

## GitLab CI Variables

These are automatically set by GitLab CI and used by the `event` command:

| Variable | Description |
|---|---|
| `GITLAB_CI` | Indicates running in GitLab CI |
| `CI_PIPELINE_SOURCE` | Pipeline trigger source (`merge_request_event`, `push`) |
| `CI_MERGE_REQUEST_IID` | Merge request IID |
| `CI_PROJECT_PATH` | Full project path (`group/project`) |
| `CI_COMMIT_SHA` | Current commit SHA |
| `CI_DEFAULT_BRANCH` | Repository default branch name |
| `CI_SERVER_URL` | GitLab instance URL |
| `CI_JOB_TOKEN` | CI job token (fallback for `GITLAB_TOKEN`) |

## ArgoCD Integration

| Variable | Required | Default | Description |
|---|---|---|---|
| `ARGOCD_SERVER_ADDR` | No | — | ArgoCD server address |
| `ARGOCD_TOKEN` | No | — | ArgoCD API token |
| `ARGOCD_PLAINTEXT` | No | `false` | Use plaintext connection to ArgoCD |
| `ARGOCD_INSECURE` | No | `false` | Skip TLS verification for ArgoCD |
