# CloudESF ASM Integration Test

This folder contains the integration test using CloudESF as ingress gateway for Istio/ASM. The detailed design is go/cloudesf-asm-e2e-testing.

## QAs

### How tests are categorized? Why it is called like apikeygrpc?

CloudESF uses the test service to differentiate tests. In one test, it could cover multiple functionality.

### Does it call mock services or real services?

It will call the real services, Chemist and IMDS.

### Where are the test client codes?

The test clients are all located [inside google3](http://google3/apiserving/cloudesf/tests/e2e/cep/clients/?rcl=392681369), wrapped as docker image.

### What are the test backend?

The test backends are all located inside google3. Currently, we only use [apikeys server](http://google3/apiserving/cloudesf/tests/e2e/endpoints/apikey_grpc/server/BUILD?l=92&rcl=392336933).

### How to run these tests locally?

To run tests locally, please:
- create a test cluster under project cloudesf-testing. The reason it has to be under cloudesf-testing because
  the test needs IAM roles to call Chemist, access images and impersonate identities to get access token.

```shell
  gcloud container clusters create ${CLUSTER_NAME} \
  --enable-ip-alias --create-subnetwork="" --network=default \
  --project=cloudesf-testing --zone=${LOCATION_ZONE} \
  --machine-type=e2-standard-4 \
  --num-nodes=2 \
  --workload-pool=cloudesf-testing.svc.id.goog
  ```

- connect to this cluster.

```shell
    gcloud container clusters get-credentials ${CLUSTER_NAME} \
   --project=cloudesf-testing \
   --zone=${LOCATION_ZONE}
```

- run test

```shell
go test -tags=integ ./tests/integration/cloudesf/apikeygrpc/...
```

## Folder patches/

These are the config applied on asm in order to install CloudESF as ingress gateway.