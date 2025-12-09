# backup-restore-openstack-mfke

Kubebuilder-based Kubernetes operator scaffold for backing up and restoring
PersistentVolumeClaims in OpenStack-based clusters (e.g., Cinder CSI).

## Description

This project provides three custom resources to model a PVC backup lifecycle:

- `Pvc`: inventory PVCs in a target namespace.
- `PvSnapshot`: request snapshots for a specific PVC.
- `RestorePvc`: restore a PVC from a previously created snapshot.

The controllers are scaffolded and ready for business logic to be added; use
this repository as a starting point to implement the actual snapshot and
restore flows that fit your OpenStack/Kubernetes environment.

### API usage and flow

- `Pvc` (spec.namespaceShoot): target namespace whose PVCs you want to inventory.
  Controller logic should list PVCs in that namespace and write their names to
  `status.pvcNames`. This CR is mostly a helper to discover PVCs that can be
  snapshotted.
- `PvSnapshot` (spec.pvcName, spec.namespace): request one or more snapshots for
  the given PVC. The reconciler should trigger your CSI snapshot logic (e.g.,
  create a `VolumeSnapshot` or use Cinder APIs) and reflect resulting snapshot
  names in `status.snapshotNames`.
- `RestorePvc` (spec.snapshotName, spec.namespace): restore a new PVC from an
  existing snapshot. The reconciler should create the PVC (and optionally a
  pod/job to hydrate it) then surface the resulting PVC name in
  `status.restoredPvcName`.

Suggested end-to-end:

1. Create a `Pvc` to inventory PVCs in a namespace.
2. For a chosen PVC name, create a `PvSnapshot` to capture a snapshot.
3. When you need to recover, create a `RestorePvc` pointing at the snapshot to
   produce a new PVC.

### Implementation sketch (controllers)

- Add RBAC to allow listing/creating PVCs, VolumeSnapshots (if used), and
  watching namespaces of interest.
- In `PvcReconciler`:
  - Fetch the CR, list PVCs in `spec.namespaceShoot`, update `status.pvcNames`.
  - Requeue periodically to keep the inventory fresh.
- In `PvSnapshotReconciler`:
  - Validate `spec` and fetch the target PVC.
  - Create snapshot resources (e.g., CSI `VolumeSnapshot`) or call OpenStack
    Cinder APIs; record identifiers in `status.snapshotNames`.
  - Handle idempotency: if snapshots already exist, do not recreate.
- In `RestorePvcReconciler`:
  - Validate `spec.snapshotName`.
  - Create a PVC (and storage class params) from the snapshot source.
  - Optionally wait for `Bound` before updating `status.restoredPvcName`.
  - Consider adding finalizers to clean up intermediate resources if needed.

## Getting Started

### Prerequisites

- go version v1.21.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/backup-restore-openstack-mfke:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/backup-restore-openstack-mfke:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
> privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

> **NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/backup-restore-openstack-mfke:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/backup-restore-openstack-mfke/<tag or branch>/dist/install.yaml
```

## Contributing

Contributions are welcome—especially around implementing the reconciliation
logic and improving the sample CRDs.

1. Fork and branch from `main`.
2. Run `make test` (or at least `make unit-test`/`make lint` if you add them) before opening a PR.
3. Keep PRs small and focused; include sample manifests for new fields.
4. Update docs (this README and `config/samples`) when behavior changes.

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
