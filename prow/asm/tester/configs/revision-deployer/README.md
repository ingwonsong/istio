# Revision deployer

## Overview

To add a new job that deploys multiple ASM Control Plane Revisions, you can create a __revision configuration__  with the following structure.

```yaml
revisions:
- name: "asm-revision-istiodca"
  ca: "CITADEL"
  overlay: "overlay/trustanchor-meshca.yaml"
- name: "asm-revision-meshca"
  ca: "MESHCA"
  overlay: "overlay/migrated-meshca.yaml"
  version: "1.9"
```

When creating a Prow job using a revision configuration, pass in the name of the config as follows.

```yaml
    spec:
      containers:
        - command:
            - entrypoint
            - ./prow/asm/integ-suite-kubetest2.sh
            ...
            - --test-flags
            - --test test.integration.asm.mdp --revision-config "my-revision-config.yaml"
```

## Configurable fields

The current per-revision configurable fields are:

* **name**: name of the revision
* **version**: ASM version to use for this revision in the form `1.X`
* **overlay**: comma-separated list of paths to configuration overlays to use for the revision. Each path is relative
  to the `configs/kpt-pkg/overlay` directory.
* **ca**: the CA type to use for this revision, either `CITADEL` or `MESHCA`.

This README should be kept up to date but for the ground-truth reference `tester/pkg/install/revision/revision.go`.

## Limitations

At the time of this writing, there are a few notable limitations:

* On each revision's install, gateway deployments from the previous installation are overwritten.
* Testing has been done primarily with GKE-on-GCP, although there's no reason it shouldn't be extensible to any environment.
    * MCP for instance: different revision semantics! We probably want to just install MCP once and then test with `--istio.test.revisions=asm-managed,asm-rapid`.
