package controller

const (
	PvcFinalizerName        = "snapshotv1beta1.mfke.io/pvc"
	PvSnapshotFinalizerName = "snapshotv1beta1.mfke.io/pvsnapshot"
	RestorePVCFinalizerName = "snapshotv1beta1.mfke.io/restorePvc"

	PvcReconcileAnnotation        = "snapshotv1beta1.mfke.io/pvc-reconcile"
	PvSnapshotReconcileAnnotation = "snapshotv1beta1.mfke.io/pvsnapshot-reconcile"
	RestorePVCReconcileAnnotation = "snapshotv1beta1.mfke.io/restorePvc-reconcile"

	VolumeSnapshotClasses = "volumesnapshotclasses"
	VolumeSnapshot        = "volumesnapshots"

	Unknown = "N/A"
)
