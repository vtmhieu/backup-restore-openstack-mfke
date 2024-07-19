package controller

const (
	PvcFinalizerName            = "snapshotv1beta1.mfke.io/pvc"
	PvSnapshotFinalizerName     = "snapshotv1beta1.mfke.io/pvsnapshot"
	RestorePVCFinalizerName     = "snapshotv1beta1.mfke.io/restorePvc"
	CreateSnapshotFinalizerName = "snapshotv1beta1.mfke.io/createSnapshot"

	PvcReconcileAnnotation            = "snapshotv1beta1.mfke.io/pvc-reconcile"
	PvSnapshotReconcileAnnotation     = "snapshotv1beta1.mfke.io/pvsnapshot-reconcile"
	CreateSnapshotReconcileAnnotation = "snapshotv1beta1.mfke.io/createSnapshot-reconcile"

	CreateSnapshotEnabledAnnotation = "snapshotv1beta1.mfke.io/createSnapshot-enabled"
	DeleteSnapshotEnabledAnnotation = "snapshotv1beta1.mfke.io/deleteSnapshot-enabled"
	RestorePVCEnabledAnnotation     = "snapshotv1beta1.mfke.io/restorePvc-enabled"

	VolumeSnapshotClasses = "volumesnapshotclasses"
	VolumeSnapshot        = "volumesnapshots"
	PersistentVolumeClaim = "persistentvolumeclaim"

	volumeSnapshotClassName = "csi-cinder-snapclass"

	Unknown = "N/A"
)
