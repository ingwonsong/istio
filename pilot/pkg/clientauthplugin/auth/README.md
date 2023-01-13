# GCP package for client auth plugin

## Why we are copying code from upstream for gcp package

The vendor specific auth plugins has been removed in <https://github.com/kubernetes/kubernetes/pull/112341>
since 09/08/2022.

The [officially suggested way](<http://cloud/blog/products/containers-kubernetes/kubectl-auth-changes-in-gke>)
for GCP is to use `gke-gcloud-auth-plugin`.
However, as we discussed in b/215156076, adding an extra token emitting binary in our CloudRun
container makes us vulnerable to Remote Code Execution. Specifically, if our Istiod container
is compromised, the attacker can use `gke-gcloud-auth-plugin` binary to obtain credentials of
the GKE cluster.

As such, we decide to keep using one single binary and import the code from somewhere else. The GKE
auth team has uploaded the code to <https://github.com/kubernetes/cloud-provider-gcp/tree/master/pkg/clientauthplugin>.
However, the code is not in a go module and the whole repo is [not recommended](<http://yaqs/4618091773170810880>) as a dependency.
While we try to figure out the solution with upstream, we decide to copy-paste the code to our
local library to unblock the merging pipeline.

## Why we are copying code from upstream for auth package

Our codebase has a lot of the following import:

```console
_ "k8s.io/client-go/plugin/pkg/client/auth"
```

The auth package simply does the underscore import of all the packages in the folder.
We need to replace this package otherwise we'll still import the client-go auth packages.
Replacing the whole package instead of expanding every import site introduces minimal
change.