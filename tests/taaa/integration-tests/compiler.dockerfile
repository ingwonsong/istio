# Image to compile the istio tests.
# Go is statically compiled but the istio tests have dependencies with
# libraries using C/C++ DLLs.
# As such this image exists in order to make sure the compiled Go is
# built with libraries consistent with the image running them.

FROM google/cloud-sdk:slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    gcc \
    wget \
    && rm -rf /var/lib/apt/lists/*
# current base image lacks go1.16 on apk, so manual install.
RUN wget -c https://golang.org/dl/go1.16.7.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go1.16.7.linux-amd64.tar.gz && \
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile

ENV GOPRIVATE='*.googlesource.com,*.git.corp.google.com'
ENV PATH=$PATH:/usr/local/go/bin
WORKDIR /usr/lib/go/src/gke-internal/istio/istio