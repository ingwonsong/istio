# chaos clusters setup

## Overview

The script aims to continuously create some chaos in a prober project so that we can test the MDP stability during rollout.
Now it just basically creates clusters, membership, installs ASM and deletes memberships and the clusters. More chaos scenarios need to be added later.

## Instructions

1. Build the image with the Dockerfile.mdpchaos
1. Push the image to the test project, e.g. iamwen-gke-dev
1. Create a cloudrun job that
   1. Runs with the image above
   1. Create a SA for the job that should have the IAM bindings of GKE Hub Admin and Kubernetes Engine Cluster Admin,
   configure the job to run with the SA
   1. Set the concurrent task number, e.g. 5.
   1. Configure the task timeout to 30mins.
1. Setup a [cloud scheduler job](https://cloud.google.com/run/docs/execute/jobs-on-schedule) to periodically trigger the cloudrun job
