FROM google/cloud-sdk:alpine

# Install prerequisites for the ASM install script http://go/scriptaro
# We actually download an install Scriptario only at runtime.
# That script takes a major and minor version and installs the version for it.

# hadolint ignore=DL3018
RUN apk add --no-cache jq coreutils && \
    rm -rf /var/cache/apk/*
RUN gcloud components install --quiet kpt kubectl

COPY * /

ENTRYPOINT ["entrypoint"]