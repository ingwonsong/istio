FROM google/cloud-sdk:alpine
RUN gcloud components install kubectl
COPY ./ /
ENTRYPOINT ["entrypoint"]
