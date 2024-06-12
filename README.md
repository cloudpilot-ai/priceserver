# Price Server

A server component used to provide RESTful APIs for querying the prices of virtual machines (such as EC2) of cloud providers.

## Pull the latest price data

Run the following commands to pull the latest price data:
```sh
go run hack/tools/pull-data/pull-latest-price.go
```

## Components Development

It is highly recommended to develop server-side components in a local environment. After testing with a demo cluster, the components can be deployed in the pre-production environment.

### Prerequisites

- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [ko](https://github.com/ko-build/ko/releases)
- [kind](https://github.com/kubernetes-sigs/kind/releases)

### Step 1: Create a Cluster

Run the following commands to create a cluster:
```bash
cat > kind-config.yaml <<EOF
# Cluster configuration with three nodes (two workers)
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
EOF

kind create cluster --name cloudpilot-dev --config kind-config.yaml
```

### Step 2: Deploy Modified Components

Initialize the manifest with the following commands, please set your corresponding keys:
```bash
export AWS_GLOBAL_ACCESS_KEY=<aws access key>
export AWS_GLOBAL_SECRET_KEY=<aws secret>
export AWS_CN_ACCESS_KEY=<aws cn access key>
export AWS_CN_SECRET_KEY=<aws cn secret key>

source hack/env.sh
hack/config-init-dev.sh
```

After initializing the manifest, deploy the modified components to the cluster:
```bash
export KO_DOCKER_REPO=kind.local
export KIND_CLUSTER_NAME=cloudpilot-dev
ko apply -f config-dev
```

Once the components are deployed, expose the service using:
```bash
kubectl port-forward svc/priceserver -n cloudpilot 8080
```

### Step 3: Testing the API

Visit corresponding API, for example, `http://localhost:8080/api/v1/aws/ec2/regions/us-east-2/price`, to test the API.
