package controller

const (
	PvcFinalizerName        = "snapshotv1beta1.mfke.io/pvc"
	PvSnapshotFinalizerName = "snapshotv1beta1.mfke.io/pvsnapshot"
	RestorePVCFinalizerName = "snapshotv1beta1.mfke.io/restorePvc"
	SnapshotFinalizerName   = "snapshotv1beta1.mfke.io/snapshot"

	PvcReconcileAnnotation        = "snapshotv1beta1.mfke.io/pvc-reconcile"
	PvSnapshotReconcileAnnotation = "snapshotv1beta1.mfke.io/pvsnapshot-reconcile"
	SnapshotReconcileAnnotation   = "snapshotv1beta1.mfke.io/snapshot-reconcile"

	CreateSnapshotEnabledAnnotation = "snapshotv1beta1.mfke.io/createSnapshot-enabled"
	DeleteSnapshotEnabledAnnotation = "snapshotv1beta1.mfke.io/deleteSnapshot-enabled"
	RestorePVCEnabledAnnotation     = "snapshotv1beta1.mfke.io/restorePvc-enabled"

	VolumeSnapshotClasses = "volumesnapshotclasses"
	VolumeSnapshot        = "volumesnapshots"
	PersistentVolumeClaim = "persistentvolumeclaim"

	volumeSnapshotClassName = "csi-cinder-snapclass"

	Unknown = "N/A"

	OneHour     = "OneHour"
	SevenDays   = "SevenDays"
	FifteenDays = "FifteenDays"
	OneMonth    = "OneMonth"
)
