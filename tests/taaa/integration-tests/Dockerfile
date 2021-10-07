# Defined by http://go/gh/istio/tools/blob/master/docker/build-tools/Dockerfile.
# Needed to have expected versions of tools used by tests.
FROM gcr.io/istio-testing/build-tools:master-latest AS tools
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
WORKDIR /

# Set the configs to the location TaaA mounts it to.
ENV CLOUDSDK_CONFIG=/root/.config/gcloud
ENV DOCKER_CONFIG=/root/.docker
ENV HOME=/root/

# Purge old entrypoint.
RUN rm -f "$(which entrypoint)"

# Copy over all built artifacts.
COPY ./ /
ENTRYPOINT ["/usr/bin/entrypoint"]
