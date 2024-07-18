package controller

const (
	PvcFinalizerName            = "snapshotv1beta1.mfke.io/pvc"
	PvSnapshotFinalizerName     = "snapshotv1beta1.mfke.io/pvsnapshot"
	RestorePVCFinalizerName     = "snapshotv1beta1.mfke.io/restorePvc"
	CreateSnapshotFinalizerName = "snapshotv1beta1.mfke.io/createSnapshot"

	PvcReconcileAnnotation            = "snapshotv1beta1.mfke.io/pvc-reconcile"
	PvSnapshotReconcileAnnotation     = "snapshotv1beta1.mfke.io/pvsnapshot-reconcile"
	RestorePVCReconcileAnnotation     = "snapshotv1beta1.mfke.io/restorePvc-reconcile"
	CreateSnapshotReconcileAnnotation = "snapshotv1beta1.mfke.io/createSnapshot-reconcile"

	VolumeSnapshotClasses = "volumesnapshotclasses"
	VolumeSnapshot        = "volumesnapshots"
	PersistentVolumeClaim = "persistentvolumeclaim"

	volumeSnapshotClassName = "csi-cinder-snapclass"

	Unknown = "N/A"
)
