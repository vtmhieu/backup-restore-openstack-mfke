# API

This directory contains the Custom Resource Definitions (CRDs) for the `backup-restore-openstack-mfke` operator. It hosts the API structs and Go types used to define the custom resources.

## Resources

- `Pvc`: Inventories PVCs in a target namespace.
- `PvSnapshot`: Requests snapshots for a specific PVC.
- `RestorePvc`: Restores a PVC from a previously created snapshot.
