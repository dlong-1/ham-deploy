// Copyright 2019 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package operator

import (
	"context"
	"reflect"

	deployv1alpha1 "github.com/hybridapp-io/ham-deploy/pkg/apis/deploy/v1alpha1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	crdRootPath          = "/usr/local/etc/hybridapp/crds/"
	crdDeployableSubPath = "core/deployable"
	crdPlacementSubPath  = "core/placement"
	crdAssemblerSubPath  = "tools/assembler"
	crdDiscovererSubPath = "tools/discoverer"
)

// Add creates a new Operator Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	reconciler := &ReconcileOperator{client: mgr.GetClient(), scheme: mgr.GetScheme()}
	reconciler.dynamicClient = dynamic.NewForConfigOrDie(mgr.GetConfig())
	return reconciler
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("deployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Operator
	err = c.Watch(&source.Kind{Type: &deployv1alpha1.Operator{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource ReplicaSet and requeue the owner Operator
	err = c.Watch(&source.Kind{Type: &appsv1.ReplicaSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &deployv1alpha1.Operator{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileOperator implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileOperator{}

// ReconcileOperator reconciles a Operator object
type ReconcileOperator struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	dynamicClient dynamic.Interface
	client        client.Client
	scheme        *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Operator object and makes changes based on the state read
// and what is in the Operator.Spec
func (r *ReconcileOperator) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	klog.Info("Reconciling Operator: ", request)

	// Fetch the Operator instance
	instance := &deployv1alpha1.Operator{}

	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue

			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// finalizer := "deploy.hybridapp.io/deleteClusterRoleResources"
	// if instance.ObjectMeta.DeletionTimestamp.IsZero() {
	// 	if !containsString(instance.ObjectMeta.Finalizers, finalizer) {
	// 		klog.Info("Adding finalizer: ", finalizer, " on resource: ", instance.Namespace, "/", instance.Name)
	// 		instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, finalizer)
	// 		if err := r.client.Update(context.TODO(), instance); err != nil {
	// 			klog.Error("Adding finalizer: ", finalizer, " on resource: ", instance.Namespace, "/", instance.Name)
	// 			return reconcile.Result{}, err
	// 		}
	// 	}
	// } else {
	// 	if containsString(instance.ObjectMeta.Finalizers, finalizer) {
	// 		klog.Info("Deleting RBAC resources for: ", instance.Namespace, "/", instance.Name)
	// 		if err := r.deleteRbacResources(); err != nil {
	// 			klog.Error("Failed to delete all RBAC resources for: ", instance.Namespace, "/", instance.Name)
	// 			return reconcile.Result{}, err
	// 		}

	// 		instance.ObjectMeta.Finalizers = removeString(instance.ObjectMeta.Finalizers, finalizer)
	// 		klog.Info("Removing finalizer: ", finalizer, " on resource: ", instance.Namespace, "/", instance.Name)
	// 		if err := r.client.Update(context.TODO(), instance); err != nil {
	// 			klog.Error("Failed to remove finalizer: ", finalizer, "error: ", err)
	// 			return reconcile.Result{}, err
	// 		}
	// 	}

	// 	// Stop reconciliation as the item is being deleted
	// 	return reconcile.Result{}, nil
	// }

	if instance.Status.Phase == "" {
		instance.Status.Phase = deployv1alpha1.PhasePending
	}

	// License must be accepted
	if !instance.Spec.LicenseSpec.Accept {
		klog.Warning("License was not accepted. (spec.license.accept = false)")

		instance.Status.Phase = deployv1alpha1.PhaseError
		instance.Status.Message = "License was not accepted"
		instance.Status.Reason = "LicenseAcceptFalse"
		updateErr := r.client.Status().Update(context.TODO(), instance)
		if updateErr != nil {
			klog.Error("Failed to update status: ", updateErr)
			return reconcile.Result{}, updateErr
		}

		return reconcile.Result{}, nil
	}

	// // CreateOrUpdate ServiceAccount
	// sa := &v1.ServiceAccount{}
	// sa.Namespace = instance.Namespace
	// sa.Name = deployv1alpha1.DefaultServiceAccountName
	// result, err := controllerruntime.CreateOrUpdate(context.TODO(), r.client, sa, func() error {
	// 	return controllerutil.SetControllerReference(instance, sa, r.scheme)
	// })
	// if err != nil {
	// 	klog.Error("Failed to reconcile ServiceAccount: ", sa.Namespace, "/", sa.Name, ", error: ", err)
	// 	return reconcile.Result{}, err
	// }
	// if result != controllerutil.OperationResultNone {
	// 	klog.Info("Reconciled ServiceAccount: ", sa.Namespace, "/", sa.Name, ", result: ", result)
	// }

	// // CreateOrUpdate ClusterRole
	// cr := &rbacv1.ClusterRole{}
	// cr.Name = deployv1alpha1.DefaultServiceAccountName // use ServiceAccount name
	// cr.Rules = clusterRoleRules
	// result, err = controllerruntime.CreateOrUpdate(context.TODO(), r.client, cr, func() error {
	// 	return nil
	// })
	// if err != nil {
	// 	klog.Error("Failed to reconcile ClusterRole: ", cr.Name, ", error: ", err)
	// 	return reconcile.Result{}, err
	// }
	// if result != controllerutil.OperationResultNone {
	// 	klog.Info("Reconciled ClusterRole: ", cr.Name, ", result: ", result)
	// }

	// // CreateOrUpdate ClusterRoleBinding
	// crb := &rbacv1.ClusterRoleBinding{}
	// crb.Name = deployv1alpha1.DefaultServiceAccountName // use ServiceAccount name
	// crb.Subjects = []rbacv1.Subject{
	// 	{
	// 		Kind:      "ServiceAccount",
	// 		Name:      deployv1alpha1.DefaultServiceAccountName, // match ServiceAccount name
	// 		Namespace: instance.Namespace,
	// 	},
	// }
	// crb.RoleRef = rbacv1.RoleRef{
	// 	APIGroup: "rbac.authorization.k8s.io",
	// 	Kind:     "ClusterRole",
	// 	Name:     deployv1alpha1.DefaultServiceAccountName, // use ServiceAccount name which matches ClusterRole name
	// }
	// result, err = controllerruntime.CreateOrUpdate(context.TODO(), r.client, crb, func() error {
	// 	return nil //TODO: Need to create a finalizer to remove; cannot be owned by controller - cluster-scoped resource must not have a namespace-scoped owner
	// })
	// if err != nil {
	// 	klog.Error("Failed to reconcile ClusterRoleBinding: ", crb.Name, ", error: ", err)
	// 	return reconcile.Result{}, err
	// }
	// if result != controllerutil.OperationResultNone {
	// 	klog.Info("Reconciled ClusterRoleBinding: ", crb.Name, ", result: ", result)
	// }

	// CreateOrUpdate ReplicaSet

	// Define a new ReplicaSet object
	rs := r.newReplicaSetForCR(instance)

	// Set Operator instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, rs, r.scheme); err != nil {
		klog.Error("Failed to set owner on ReplicaSet: ", rs.Name, " Namespace:", rs.Namespace)
		return reconcile.Result{}, err
	}

	// Check if this ReplicaSet already exists
	found := &appsv1.ReplicaSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: rs.Name, Namespace: rs.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		klog.Info("Creating a new Replicaset: ", rs.Name, " Namespace: ", rs.Namespace)
		err = r.client.Create(context.TODO(), rs)

		if err != nil {
			klog.Error("Failed to create new ReplicaSet, error:", err)
			return reconcile.Result{}, err
		}

		// ReplicaSet created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// ReplicaSet already exists - try to update
	uptodate := isEqualReplicaSetPods(found, rs)

	if !uptodate {
		klog.Info("Pod has changed; deleting Replicaset: ", rs.Name, " Namespace: ", rs.Namespace)
		err = r.client.Delete(context.TODO(), found)
		if err != nil {
			klog.Error("Failed to delete existing replica, error:", err)
		}
		return reconcile.Result{}, err
	}

	if *rs.Spec.Replicas != *found.Spec.Replicas {
		found.Spec.Replicas = rs.Spec.Replicas

		klog.Info("Updating # of replicas for Replicaset: ", rs.Name, " Namespace: ", rs.Namespace)
		err = r.client.Update(context.TODO(), found)

		if err != nil {
			klog.Error("Failed to update # of replicas, error:", err)
		}
		return reconcile.Result{}, err
	}

	// update deployment status
	if instance.Status.Phase != deployv1alpha1.PhaseInstalled {
		instance.Status.Phase = deployv1alpha1.PhaseInstalled
		instance.Status.Message = ""
		instance.Status.Reason = ""
	}
	instance.Status.ReplicaSetStatus = found.Status.DeepCopy()
	err = r.client.Status().Update(context.TODO(), instance)

	return reconcile.Result{}, err
}

func (r *ReconcileOperator) createReplicaSet(cr *deployv1alpha1.Operator) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
		},
	}

	if cr.Spec.Replicas == nil {
		rs.Spec.Replicas = &deployv1alpha1.DefaultReplicas
	} else {
		rs.Spec.Replicas = cr.Spec.Replicas
	}

	rs.Spec.Template.Name = cr.Name
	rs.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": cr.Name},
	}

	if cr.Labels == nil {
		rs.Spec.Template.Labels = map[string]string{"app": cr.Name}
	} else {
		rs.Spec.Template.Labels = cr.Labels
		rs.Spec.Template.Labels["app"] = cr.Name
	}

	if cr.Annotations != nil {
		rs.Spec.Template.Annotations = cr.Annotations
	}

	rs.Spec.Template.Spec.ServiceAccountName = deployv1alpha1.DefaultServiceAccountName

	// inherit operator settings if possible
	opns, err := k8sutil.GetOperatorNamespace()
	if err == nil {
		oppod, err := k8sutil.GetPod(context.TODO(), r.client, opns)

		if err == nil {
			oppod.Spec.Containers = nil
			oppod.Spec.NodeName = ""
			oppod.Spec.DeepCopyInto(&rs.Spec.Template.Spec)
		}
	}

	return rs
}

func (r *ReconcileOperator) configPodByCoreSpec(spec *deployv1alpha1.CoreSpec, rs *appsv1.ReplicaSet) *appsv1.ReplicaSet {
	var exists, implied bool

	// add deployable container unless spec.CoreSpec.DeployableOperatorSpec.Enabled = false
	exists = spec != nil && spec.DeployableOperatorSpec != nil
	implied = spec == nil || spec.DeployableOperatorSpec == nil || spec.DeployableOperatorSpec.Enabled == nil

	if implied || *(spec.DeployableOperatorSpec.Enabled) {
		var dospec *deployv1alpha1.DeployableOperatorSpec

		if exists {
			dospec = spec.DeployableOperatorSpec
		} else {
			dospec = &deployv1alpha1.DeployableOperatorSpec{}
		}

		rs.Spec.Template.Spec.Containers = append(rs.Spec.Template.Spec.Containers, *r.generateDeployableContainer(dospec))
	}

	// add placement container unless spec.CoreSpec.PlacementSpec.Enabled = false
	exists = spec != nil && spec.PlacementSpec != nil
	implied = spec == nil || spec.PlacementSpec == nil || spec.PlacementSpec.Enabled == nil

	if implied || *(spec.PlacementSpec.Enabled) {
		var pspec *deployv1alpha1.PlacementSpec

		if exists {
			pspec = spec.PlacementSpec
		} else {
			pspec = &deployv1alpha1.PlacementSpec{}
		}

		rs.Spec.Template.Spec.Containers = append(rs.Spec.Template.Spec.Containers, *r.generatePlacementContainer(pspec))
	}

	return rs
}

func (r *ReconcileOperator) configPodByToolsSpec(spec *deployv1alpha1.ToolsSpec, rs *appsv1.ReplicaSet) *appsv1.ReplicaSet {
	var exists, implied bool

	// add assembler container unless spec.ToolsSpec.ApplicationAssemblerSpec.Enabled = false
	exists = spec != nil && spec.ApplicationAssemblerSpec != nil
	implied = spec == nil || spec.ApplicationAssemblerSpec == nil || spec.ApplicationAssemblerSpec.Enabled == nil

	if implied || *(spec.ApplicationAssemblerSpec.Enabled) {
		var aaspec *deployv1alpha1.ApplicationAssemblerSpec

		if exists {
			aaspec = spec.ApplicationAssemblerSpec
		} else {
			aaspec = &deployv1alpha1.ApplicationAssemblerSpec{}
		}

		rs.Spec.Template.Spec.Containers = append(rs.Spec.Template.Spec.Containers, *r.generateAssemblerContainer(aaspec))
	}

	// add discoverer container only if spec.ToolsSpec.ResourceDiscovererSpec.Enabled =
	exists = spec != nil && spec.ResourceDiscovererSpec != nil && spec.ResourceDiscovererSpec.Enabled != nil

	if exists && *(spec.ResourceDiscovererSpec.Enabled) {
		rdspec := spec.ResourceDiscovererSpec

		rs.Spec.Template.Spec.Containers = append(rs.Spec.Template.Spec.Containers, *r.generateDiscovererContainer(rdspec, rs))
	}

	return rs
}

// newPodForCR returns a pod with the same name/namespace as the cr
func (r *ReconcileOperator) newReplicaSetForCR(cr *deployv1alpha1.Operator) *appsv1.ReplicaSet {
	rs := r.createReplicaSet(cr)

	rs = r.configPodByCoreSpec(cr.Spec.CoreSpec, rs)
	rs = r.configPodByToolsSpec(cr.Spec.ToolsSpec, rs)

	return rs
}

func isEqualReplicaSetPods(oldrs, newrs *appsv1.ReplicaSet) bool {
	if !isEqualVolumes(oldrs.Spec.Template.Spec.Volumes, newrs.Spec.Template.Spec.Volumes) {
		return false
	}

	// compare containers
	oldctnmap := make(map[string]*corev1.Container)
	for _, ctn := range oldrs.Spec.Template.Spec.Containers {
		oldctnmap[ctn.Name] = ctn.DeepCopy()
	}

	for _, ctn := range newrs.Spec.Template.Spec.Containers {
		octn, ok := oldctnmap[ctn.Name]
		if !ok {
			return false
		}

		if !isEqualContainer(octn, ctn.DeepCopy()) {
			return false
		}

		delete(oldctnmap, ctn.Name)
	}

	return len(oldctnmap) == 0
}

func isEqualVolumes(oldvols, newvols []corev1.Volume) bool {
	// compare volumns
	volmap := make(map[string]*corev1.Volume)
	for _, vol := range oldvols {
		volmap[vol.Name] = vol.DeepCopy()
	}

	for _, vol := range newvols {
		if oldvol, ok := volmap[vol.Name]; !ok {
			return false
		} else if !reflect.DeepEqual(*oldvol, vol) {
			return false
		}

		delete(volmap, vol.Name)
	}

	// if all new volumes are added, we're good. ignore the volumes generated by system
	return true
}

func isEqualContainer(oldctn, newctn *corev1.Container) bool {
	if (oldctn == newctn) || (oldctn == nil && newctn == nil) {
		return true
	}

	if oldctn == nil || newctn == nil {
		return false
	}

	if oldctn.Name != newctn.Name {
		return false
	}

	if oldctn.Image != newctn.Image {
		return false
	}

	if !isEqualStringArray(oldctn.Command, newctn.Command) {
		return false
	}

	volmtmap := make(map[string]*corev1.VolumeMount)
	for _, volm := range oldctn.VolumeMounts {
		volmtmap[volm.Name] = volm.DeepCopy()
	}

	for _, volm := range newctn.VolumeMounts {
		if oldvolm, ok := volmtmap[volm.Name]; !ok {
			return false
		} else if !reflect.DeepEqual(oldvolm, volm) {
			return false
		}
	}

	return isEqualStringArray(oldctn.Args, newctn.Args)
}

func isEqualStringArray(sa1, sa2 []string) bool {
	if sa1 == nil && sa2 == nil {
		return true
	}

	if sa1 == nil || sa2 == nil {
		return false
	}

	samap1 := make(map[string]string)
	for _, s := range sa1 {
		samap1[s] = s
	}

	for _, s := range sa2 {
		if _, ok := samap1[s]; !ok {
			return false
		}

		delete(samap1, s)
	}

	return len(samap1) == 0
}

// func (r *ReconcileOperator) deleteRbacResources() error {
// 	cr := &rbacv1.ClusterRole{}
// 	err := r.client.Get(context.TODO(), types.NamespacedName{Name: deployv1alpha1.DefaultServiceAccountName}, cr)
// 	if err != nil {
// 		if errors.IsNotFound(err) {
// 			return nil
// 		}
// 	}
// 	if err := utils.DeleteClusterRole(r.client, cr); err != nil {
// 		if !errors.IsNotFound(err) {
// 			klog.Error("Failed to delete ClusterRole: ", cr.Name, ", error: ", err)
// 			return err
// 		}
// 	}

// 	crb := &rbacv1.ClusterRoleBinding{}
// 	err = r.client.Get(context.TODO(), types.NamespacedName{Name: deployv1alpha1.DefaultServiceAccountName}, crb)
// 	if err != nil {
// 		if errors.IsNotFound(err) {
// 			return nil
// 		}
// 	}
// 	if err := utils.DeleteClusterRoleBinding(r.client, crb); err != nil {
// 		if !errors.IsNotFound(err) {
// 			klog.Error("Failed to delete ClusterRoleBinding: ", crb.Name, ", error: ", err)
// 			return err
// 		}
// 	}
// 	return nil
// }

// func containsString(slice []string, s string) bool {
// 	for _, item := range slice {
// 		if item == s {
// 			return true
// 		}
// 	}
// 	return false
// }

// func removeString(slice []string, s string) (result []string) {
// 	for _, item := range slice {
// 		if item == s {
// 			continue
// 		}
// 		result = append(result, item)
// 	}
// 	return
// }
