<!-- markdownlint-disable MD033 -->
<p align="center">  <img src="https://user-images.githubusercontent.com/1616153/217223509-9aa60a5c-a263-41a7-814d-a1bf2957acf6.png " width="150"> </p>

# <p align="center"> Telefonistka</p>

<!-- markdownlint-enable MD033 -->

> **This is a fork of [commercetools/telefonistka](https://github.com/commercetools/telefonistka) extended to add native GitLab support (MR events, GitLab CI pipeline mode). The upstream project supports GitHub only.**

Telefonistka is a webhook server/Bot that facilitates change promotion across environments/failure domains in Infrastructure as Code(IaC) GitOps repos.

It assumes the [repeatable part of your infrastructure is modeled in folders](#modeling-environmentsfailure-domains-in-an-iac-gitops-repo)

Based on configuration in the IaC repo, the bot will open pull requests/merge requests that sync components from "sourcePaths" to "targetPaths".

Providing reasonably flexible control over what is promoted to where and in what order.

## Modeling environments/failure-domains in an IaC GitOps repo

RY is the new DRY!

In GitOps IaC implementations, different environments(`dev`/`prod`/...) and failure domains(`us-east-1`/`us-west-1`/...) must be represented in distinct files, folders, Git branches or even repositories to allow gradual and controlled rollout of changes across said environments/failure domains.

A common folder structure:

```text
clusters/dev/demo-app/
clusters/staging/demo-app/
clusters/prod/us-east-1/demo-app/
clusters/prod/eu-west-1/demo-app/
```

While this approach provides multiple benefits it does mean the user is expected to make changes in multiple files and folders in order to apply a single change to multiple environments/FDs.

Telefonistka will automagically create PRs/MRs that "sync" changes to the right folder or folders, enabling the usage of the familiar PR/MR functionality to control promotions while avoiding the toil related to manually syncing directories and checking for drift.

## Notable Features

### IaC stack agnostic

Terraform, Helmfile, whatever — as long as environments and sites are modeled as folders and components are copied between environments "as is".

### Unopinionated directory structure

The [in-repo configuration file](#repo-configuration-telefonistkayaml) is flexible and even has some regex support.

### Multi stage promotion schemes

```text
workspace -> dev -> staging
```

or

```text
dev -> production-us-east-1 -> production-us-east-3 -> production-eu-east-1
```

Fan out, like:

```text
workspace -> dev  -->
             staging1 -->  production
             staging2 -->
```

### Control granularity of promotion PRs/MRs

Allows separating promotions into separate PRs/MRs per environment/failure domain or grouping some/all of them.

Also allows automatic merging of PRs/MRs based on the promotion policy.

### Drift detection and warning

Warns on drift between environment/failure domains on open PRs/MRs ("Staging and Production are not synced, these are the differences").

### Artifact version bumping from CLI

```shell
telefonistka bump-overwrite \
    --target-repo org/my-iac-repo \
    --target-file workspace/nginx/values-version.yaml \
    --file <(echo -e "image:\n  tag: v3.4.9")
```

## Usage

### Repo configuration (`telefonistka.yaml`)

Place a `telefonistka.yaml` at the root of your IaC repo:

```yaml
promotionPaths:
  # First promotion: workspace → dev (auto-merge)
  - sourcePath: "workspace/"
    conditions:
      autoMerge: true
    promotionPrs:
      - targetDescription: "Development Environment"
        targetPaths:
          - "clusters/dev/"

  # Second promotion: dev → staging
  - sourcePath: "clusters/dev/"
    promotionPrs:
      - targetDescription: "Staging Environment"
        targetPaths:
          - "clusters/staging/"

dryRunMode: false
autoApprovePromotionPrs: false
```

---

### GitHub — GitHub Actions workflow

Add `.github/workflows/telefonistka.yaml` to your IaC repo:

```yaml
name: Telefonistka
on:
  pull_request:
    types:
      - closed
      - opened
      - synchronize
    branches: ["main"]

jobs:
  telefonistka:
    runs-on: ubuntu-latest
    container:
      image: docker.io/schubergphilis/container-platform-telefonistka
    steps:
      - name: Run Telefonistka
        run: telefonistka event
        env:
          GITHUB_OAUTH_TOKEN: ${{ secrets.GH_TOKEN }}
          APPROVER_GITHUB_OAUTH_TOKEN: ${{ secrets.GH_TOKEN }}
          TEMPLATES_PATH: "/srv/templates/"
```

**Required secrets:**

- `GH_TOKEN` — GitHub personal access token with `repo` scope

---

### GitLab — GitLab CI pipeline

Add `.gitlab-ci.yml` to your IaC repo:

```yaml
variables:
  TELEFONISTKA_IMAGE: "docker.io/schubergphilis/container-platform-telefonistka"

stages:
  - promotion

telefonistka:mr:
  stage: promotion
  image:
    name: $TELEFONISTKA_IMAGE
    entrypoint: [""]
  script:
    - telefonistka event --provider gitlab
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
      when: always
  variables:
    GITLAB_TOKEN: $TELEFONISTKA_GITLAB_TOKEN
    GITLAB_URL: $CI_SERVER_URL
    LOG_LEVEL: info
  allow_failure: false

telefonistka:push:
  stage: promotion
  image:
    name: $TELEFONISTKA_IMAGE
    entrypoint: [""]
  interruptible: false
  script:
    - telefonistka event --provider gitlab
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
      when: always
  variables:
    GITLAB_TOKEN: $TELEFONISTKA_GITLAB_TOKEN
    GITLAB_URL: $CI_SERVER_URL
    LOG_LEVEL: info
  allow_failure: false
```

**Required CI/CD variables:**

- `TELEFONISTKA_GITLAB_TOKEN` — GitLab personal access token with `api` scope

---

## Observability

See [here](docs/observability.md)

## Development

Telefonistka has 3 major methods to interact with the world:

- Receive event webhooks from GitHub or GitLab
- Send API calls to GitHub/GitLab REST and GraphQL APIs (requires network access and credentials)

Supporting all those requirements in a local environment might require lots of setup. Assuming you have a working lab environment, the easiest way to locally test Telefonistka might be with tools like [mirrord](https://mirrord.dev/) or [telepresence](https://www.telepresence.io/).

Alternatively, you can use `ngrok` or similar services to route webhooks to a local instance.

## Roadmap

See the [open issues](https://github.com/schubergphilis/container-platform-telefonistka/issues) for a list of proposed features (and known issues).

## License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.
