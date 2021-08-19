FROM google/cloud-sdk:alpine
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
RUN gcloud components install kubectl
# Install docker credential helper.
ENV DOCKER_CRED_VERSION=2.0.0
RUN curl -fsSL "https://github.com/GoogleCloudPlatform/docker-credential-gcr/releases/download/v${DOCKER_CRED_VERSION}/docker-credential-gcr_linux_amd64-${DOCKER_CRED_VERSION}.tar.gz" \
    | tar xz --to-stdout ./docker-credential-gcr \
    > /usr/bin/docker-credential-gcr && chmod +x /usr/bin/docker-credential-gcr
COPY ./ /
ENTRYPOINT ["entrypoint"]
