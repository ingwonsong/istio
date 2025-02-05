# CSM re-generate protos to include OpenCensus

```
git clone https://gke-internal.googlesource.com/istio/envoy
cd ./envoy && git checkout master-asm
```

Update `LLVM_ROOT` in `ci/build_setup.sh` to `/usr`

`cd ../go-control-plane` and comment out `commit_changes` command so it doesn't attempt to push to upstream.

Generate protos - `ENVOY_SRC_DIR=../envoy ./ci/sync_envoy.sh`

Undo changes to bash script `git restore ./ci/sync_envoy.sh`

Commit changes `git commit -m "[CSM] Regenerate protos $(git rev-parse --short HEAD)"`

Copy the whole folder to `istio/istio` repo
```
cd ../istio
cp -R ../go-control-plane ./
```