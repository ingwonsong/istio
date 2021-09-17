##############
# Prow Image #
##############
# Defined by http://go/gh/istio/tools/blob/master/docker/build-tools/Dockerfile.
# Needed to have expected versions of tools used by tests.
FROM gcr.io/istio-testing/build-tools:master-latest AS tools
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
WORKDIR /toolsbin
RUN cp /usr/bin/helm* \
    /usr/bin/kubectl \
    /usr/bin/kpt \
    /toolsbin

###############
# Final Image #
###############
FROM google/cloud-sdk:slim
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
WORKDIR /
# TARGETARCH is an automatic platform ARG enabled by Docker BuildKit.
ARG TARGETARCH

# hadolint ignore=DL3018
RUN apt-get update && apt-get install -y --no-install-recommends \
    jq \
    coreutils \
    && rm -rf /var/lib/apt/lists/*
# Install docker credential helper.
ENV DOCKER_CRED_VERSION=2.0.0
RUN curl -fsSL "https://github.com/GoogleCloudPlatform/docker-credential-gcr/releases/download/v${DOCKER_CRED_VERSION}/docker-credential-gcr_linux_amd64-${DOCKER_CRED_VERSION}.tar.gz" \
    | tar xz --to-stdout ./docker-credential-gcr \
    > /usr/bin/docker-credential-gcr && chmod +x /usr/bin/docker-credential-gcr

COPY --from=tools /toolsbin/ /usr/bin/

# Copy over all built artifacts.
COPY ./ /
ENTRYPOINT ["entrypoint"]
