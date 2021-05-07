#!/bin/bash
# Copyright Google
# not licensed under Apache License, Version 2
set -euxo pipefail

# build-tools image doesn't have rsync‽
# once we deprecate taaa/lib/pkg/registry for new functionality in crane this can be removed
apt-get update
apt-get install rsync

git config --add --global url."https://gke-internal.googlesource.com".insteadOf sso://gke-internal.git.corp.google.com
git config --add --global url."https://gke-internal.googlesource.com".insteadOf https://gke-internal.git.corp.google.com
git config --add --global url."https://gke-internal.googlesource.com".insteadOf git://gke-internal.git.corp.google.com
git config --add --global url."https://gke-internal.googlesource.com".insteadOf git+ssh://gke-internal.git.corp.google.com
git config --add --global url."https://gke-internal.googlesource.com".insteadOf ssh://gke-internal.git.corp.google.com
git config --add --global url."https://gke-internal.googlesource.com".insteadOf sso://gke-internal.googlesource.com
git config --global user.name "${GIT_USER_NAME}"
git config --global user.email "${GIT_USER_EMAIL}"
git config --global http.cookiefile "${GIT_HTTP_COOKIEFILE}"
export GOPRIVATE='*.googlesource.com,*.git.corp.google.com'

pushd /home/prow/go/src/github.com/magefile/mage
go run bootstrap.go
popd
cd tests/taaa
