##############
# Prow Image #
##############
# Defined by http://go/gh/istio/tools/blob/master/docker/build-tools/Dockerfile.
# Needed to have expected versions of tools used by tests.
FROM gcr.io/istio-testing/build-tools:master-latest AS tools
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
WORKDIR /toolsbin
RUN cp \
    /usr/bin/helm* \
    /usr/bin/kubectl \
    /usr/bin/kpt \
    /usr/bin/docker-credential-gcr \
    /toolsbin

###############
# Final Image #
###############
FROM google/cloud-sdk:slim
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
WORKDIR /

# hadolint ignore=DL3018
RUN apt-get update && apt-get install -y --no-install-recommends \
    jq \
    coreutils \
    && rm -rf /var/lib/apt/lists/*

COPY --from=tools /toolsbin/ /usr/bin/

# Copy over all built artifacts.
COPY ./ /
ENTRYPOINT ["entrypoint"]
