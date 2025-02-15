package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/RHEcosystemAppEng/dbaas-operator/api/v1alpha1"
)

// InstallNamespaceEnvVar is the constant for env variable INSTALL_NAMESPACE
var InstallNamespaceEnvVar = "INSTALL_NAMESPACE"

type DBaaSReconciler struct {
	client.Client
	*runtime.Scheme
	InstallNamespace string
}

func (p *DBaaSReconciler) getDBaaSProvider(providerName string, ctx context.Context) (v1alpha1.DBaaSProvider, error) {
	var provider v1alpha1.DBaaSProvider
	if err := p.Get(ctx, types.NamespacedName{Name: providerName}, &provider); err != nil {
		return v1alpha1.DBaaSProvider{}, err
	}
	return provider, nil
}

func (p *DBaaSReconciler) watchDBaaSProviderObject(ctrl controller.Controller, object runtime.Object, providerObjectKind string) error {
	providerObject := unstructured.Unstructured{}
	providerObject.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   v1alpha1.GroupVersion.Group,
		Version: v1alpha1.GroupVersion.Version,
		Kind:    providerObjectKind,
	})
	err := ctrl.Watch(
		&source.Kind{
			Type: &providerObject,
		},
		&handler.EnqueueRequestForOwner{
			OwnerType:    object,
			IsController: true,
		},
	)
	if err != nil {
		return err
	}
	return nil
}

func (p *DBaaSReconciler) createProviderObject(object client.Object, providerObjectKind string) *unstructured.Unstructured {
	var providerObject unstructured.Unstructured
	providerObject.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   v1alpha1.GroupVersion.Group,
		Version: v1alpha1.GroupVersion.Version,
		Kind:    providerObjectKind,
	})
	providerObject.SetNamespace(object.GetNamespace())
	providerObject.SetName(object.GetName())
	return &providerObject
}

func (p *DBaaSReconciler) reconcileProviderObject(providerObject *unstructured.Unstructured, mutateFn controllerutil.MutateFn, ctx context.Context) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, p.Client, providerObject, mutateFn)
}

func (p *DBaaSReconciler) providerObjectMutateFn(object client.Object, providerObject *unstructured.Unstructured, spec interface{}) controllerutil.MutateFn {
	return func() error {
		providerObject.UnstructuredContent()["spec"] = spec
		providerObject.SetOwnerReferences(nil)
		if err := ctrl.SetControllerReference(object, providerObject, p.Scheme); err != nil {
			return err
		}
		return nil
	}
}

func (p *DBaaSReconciler) parseProviderObject(object interface{}, unstructured *unstructured.Unstructured) error {
	b, err := unstructured.MarshalJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, object)
	if err != nil {
		return err
	}
	return nil
}

func (p *DBaaSReconciler) reconcileDBaaSObjectStatus(object client.Object, ctx context.Context, f controllerutil.MutateFn) error {
	if err := f(); err != nil {
		return err
	}
	return p.Status().Update(ctx, object)
}

func (r *DBaaSReconciler) createObject(k8sObj, owner client.Object, ctx context.Context) error {
	if err := ctrl.SetControllerReference(owner, k8sObj, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, k8sObj); err != nil {
		return err
	}
	return nil
}

func (r *DBaaSReconciler) updateObject(k8sObj client.Object, ctx context.Context) error {
	if err := r.Update(ctx, k8sObj); err != nil {
		return err
	}
	return nil
}

// create RBAC object, return true if already exists
func (r *DBaaSReconciler) createRbacObj(newObj, getObj, owner client.Object, ctx context.Context) (exists bool, err error) {
	name := newObj.GetName()
	namespace := newObj.GetNamespace()
	logger := ctrl.LoggerFrom(ctx, owner.GetObjectKind().GroupVersionKind().Kind+" RBAC", types.NamespacedName{Name: name, Namespace: namespace})
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, getObj); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("resource not found", name, namespace)
			if err = r.createObject(newObj, owner, ctx); err != nil {
				logger.Error(err, "Error creating resource", name, namespace)
				return false, err
			}
			logger.V(1).Info("resource created", name, namespace)
		} else {
			logger.Error(err, "Error getting the resource", name, namespace)
			return false, err
		}
	} else {
		return true, nil
	}
	return false, nil
}

// GetInstallNamespace returns the operator's install Namespace
func GetInstallNamespace() (string, error) {
	ns, found := os.LookupEnv(InstallNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", InstallNamespaceEnvVar)
	}
	return ns, nil
}

// create an rbac subject for use in role bindings
func getSubject(name, namespace, rbacObjectKind string) rbacv1.Subject {
	return rbacv1.Subject{
		APIGroup:  rbacv1.SchemeGroupVersion.Group,
		Kind:      rbacObjectKind,
		Name:      name,
		Namespace: namespace,
	}
}

// returns a unique subset of the provided slice
func uniqueStr(input []string) []string {
	u := make([]string, 0, len(input))
	m := make(map[string]bool)

	for _, val := range input {
		if _, ok := m[val]; !ok {
			m[val] = true
			u = append(u, val)
		}
	}

	return u
}

// checks if a string is present in a slice
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}
