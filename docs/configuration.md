# Configuration Reference

Telefonistka is configured via a `telefonistka.yaml` file in the root of your repository.

## Full Schema

```yaml
# Default branch name (optional, defaults to repo's default branch)
defaultBranch: main

# Labels to add to promotion PRs/MRs
promotionPRLabels:            # Note: the old YAML key "promtionPRlables" is still accepted for backward compatibility
  - promotion
  - automated

# Run in dry-run mode (comment plan instead of executing)
dryRunMode: false

# Automatically approve promotion PRs/MRs (requires approver token)
autoApprovePromotionPrs: false

# Commit status toggling (provider-specific)
toggleCommitStatus: {}

# Promotion paths define which directories trigger promotions
promotionPaths:
  - sourcePath: "env/dev/"        # Regex pattern matching source directories
    componentPathExtraDepth: 0    # Extra path depth for component identification
    conditions:
      prHasLabels:                # Only promote if PR/MR has these labels
        - promote
      autoMerge: false            # Auto-merge the promotion PR/MR
    promotionPrs:
      - targetPaths:              # Where to promote to
          - "env/staging/"
        targetDescription: "staging"  # Human-readable description
        blockList:                    # Glob patterns for files to exclude from sync
          - "*.tmp"

# Webhook endpoint multiplexing (GitHub push events only)
webhookEndpointRegexs:
  - expression: "^clusters/([^/]+)/.*"
    replacements:
      - "https://argocd-$1.example.com/api/webhook"

# Skip TLS verification for upstream webhook endpoints
whProxtSkipTLSVerifyUpstream: false

# ArgoCD integration
argocd:
  commentDiffonPR: false
  autoMergeNoDiffPRs: false
  allowSyncfromBranchPathRegex: ""
  useSHALabelForAppDiscovery: false
  createTempAppObjectFromNewApps: false
```

## Component Configuration

Individual components can have their own configuration in a `telefonistka.yaml` within the component directory:

```yaml
# Block promotion to specific targets
promotionTargetBlockList:
  - "env/prod/.*"

# Allow promotion only to specific targets
promotionTargetAllowList:
  - "env/staging/.*"

# Disable ArgoCD diff for this component
disableArgoCDDiff: false
```
