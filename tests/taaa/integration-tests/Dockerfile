FROM google/cloud-sdk:slim
SHELL ["/bin/bash", "-eo", "pipefail", "-c"]
# TARGETARCH is an automatic platform ARG enabled by Docker BuildKit.
ARG TARGETARCH

# hadolint ignore=DL3018
RUN apt-get update && apt-get install -y --no-install-recommends \
    jq \
    google-cloud-sdk-kpt \
    kubectl \
    coreutils \
    && rm -rf /var/lib/apt/lists/*
# Install docker credential helper.
ENV DOCKER_CRED_VERSION=2.0.0
RUN curl -fsSL "https://github.com/GoogleCloudPlatform/docker-credential-gcr/releases/download/v${DOCKER_CRED_VERSION}/docker-credential-gcr_linux_amd64-${DOCKER_CRED_VERSION}.tar.gz" \
    | tar xz --to-stdout ./docker-credential-gcr \
    > /usr/bin/docker-credential-gcr && chmod +x /usr/bin/docker-credential-gcr
# Helm version 3.
ENV HELM3_VERSION=v3.4.2
ADD https://get.helm.sh/helm-${HELM3_VERSION}-linux-${TARGETARCH}.tar.gz /tmp
RUN mkdir "/tmp/helm3" && \
    tar -xf "/tmp/helm-${HELM3_VERSION}-linux-${TARGETARCH}.tar.gz" -C "/tmp/helm3" && \
    cp "/tmp/helm3/linux-${TARGETARCH}/helm" "${OUTDIR}/usr/bin/helm3" && \
    mv "/tmp/helm3/linux-${TARGETARCH}/helm" "${OUTDIR}/usr/bin/helm"
# Copy over all built artifacts.
COPY ./ /
ENTRYPOINT ["entrypoint"]
