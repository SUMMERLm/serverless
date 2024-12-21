package common

import (
	"context"

	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	//"github.com/lmxia/gaia/pkg/apis/apps/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CdnSupplier is a cdn resource
type CdnSupplier struct {
	metaV1.TypeMeta   `json:",inline"`
	metaV1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CdnSupplierSpec `json:"spec"`
	Status            SupplierStatus  `json:"status,omitempty"`
}

type CdnSupplierSpec struct {
	// +required
	SupplierName string `json:"supplierName,omitempty"`
	// +required
	CloudAccessKeyid string `json:"cloudAccessKeyid,omitempty"`
	// +required
	CloudAccessKeysecret string `json:"cloudAccessKeysecret,omitempty"`
}

type SupplierStatus struct{}

type CdnProvider struct {
	// +required
	SupplierName string `json:"supplierName,omitempty"`
	// +required
	CloudAccessKeyid string `json:"cloudAccessKeyid,omitempty"`
	// +required
	CloudAccessKeysecret string `json:"cloudAccessKeysecret,omitempty"`
}

type CdnProviderName struct {
	// +required
	SupplierName string `json:"supplierName,omitempty"`
}

func (cdnProvider *CdnProvider) Register() bool {
	klog.Infof(cdnProvider.SupplierName)
	go cdnProvider.doRegister()
	return true
}

func (cdnProvider *CdnProvider) doRegister() bool {
	cdnGvr := schema.GroupVersionResource{Group: "apps.gaia.io", Version: "v1alpha1", Resource: "cdnsuppliers"}
	resultCdn, err := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Get(context.TODO(), cdnProvider.SupplierName, metaV1.GetOptions{})
	cdnSupplier := CdnSupplier{}
	if errUnstructured := runtime.DefaultUnstructuredConverter.FromUnstructured(resultCdn.UnstructuredContent(), cdnSupplier); err != nil {
		klog.Error(errUnstructured)
	}
	if err != nil {
		klog.Infof("new cdn provider")
		resultCdn := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps.gaia.io/v1alpha1",
				"kind":       "cdnsuppliers",
				"spec": map[string]interface{}{
					"supplierName":         cdnProvider.SupplierName,
					"cloudAccessKeyid":     cdnProvider.CloudAccessKeyid,
					"cloudAccessKeysecret": cdnProvider.CloudAccessKeysecret,
				},
			},
		}
		_, newCdnErr := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Create(context.TODO(), resultCdn, metaV1.CreateOptions{})
		if newCdnErr != nil {
			klog.Info(newCdnErr)
			return false
		}
	} else if cdnSupplier.Spec.CloudAccessKeyid != cdnProvider.CloudAccessKeyid || cdnSupplier.Spec.CloudAccessKeysecret != cdnProvider.CloudAccessKeysecret {
		resultCdn := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps.gaia.io/v1alpha1",
				"kind":       "cdnsuppliers",
				"spec": map[string]interface{}{
					"supplierName":         cdnProvider.SupplierName,
					"cloudAccessKeyid":     cdnProvider.CloudAccessKeyid,
					"cloudAccessKeysecret": cdnProvider.CloudAccessKeysecret,
				},
			},
		}
		_, updateCdnErr := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Update(context.TODO(), resultCdn, metaV1.UpdateOptions{})
		if updateCdnErr != nil {
			klog.Info(updateCdnErr)
			return false
		}
	}
	return true
}

func (cdnProvider *CdnProvider) Recycle() bool {
	klog.Infof(cdnProvider.SupplierName)
	go cdnProvider.doRecycle()
	return true
}

func (cdnProvider *CdnProvider) doRecycle() bool {
	cdnGvr := schema.GroupVersionResource{Group: "apps.gaia.io", Version: "v1alpha1", Resource: "cdnsuppliers"}
	resultCdn, err := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Get(context.TODO(), cdnProvider.SupplierName, metaV1.GetOptions{})
	cdnSupplier := CdnSupplier{}
	if errUnstructured := runtime.DefaultUnstructuredConverter.FromUnstructured(resultCdn.UnstructuredContent(), cdnSupplier); err != nil {
		klog.Error(errUnstructured)
	}
	if err != nil {
		klog.Infof("new cdn provider")
		deleteCdnErr := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Delete(context.TODO(), cdnProvider.SupplierName, metaV1.DeleteOptions{})
		if deleteCdnErr != nil {
			klog.Info(deleteCdnErr)
			return false
		}
	}
	return true
}

func (cdnProvider *CdnProvider) Update() bool {
	klog.Infof(cdnProvider.SupplierName)
	go cdnProvider.doUpadte()
	return true
}

func (cdnProvider *CdnProvider) doUpadte() bool {
	cdnGvr := schema.GroupVersionResource{Group: "apps.gaia.io", Version: "v1alpha1", Resource: "cdnsuppliers"}
	resultCdn, err := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Get(context.TODO(), cdnProvider.SupplierName, metaV1.GetOptions{})
	cdnSupplier := CdnSupplier{}
	if errUnstructured := runtime.DefaultUnstructuredConverter.FromUnstructured(resultCdn.UnstructuredContent(), cdnSupplier); err != nil {
		klog.Error(errUnstructured)
	}
	if err != nil {
		klog.Infof("cdn provider is not exist")
		return false
	} else if cdnSupplier.Spec.CloudAccessKeyid != cdnProvider.CloudAccessKeyid || cdnSupplier.Spec.CloudAccessKeysecret != cdnProvider.CloudAccessKeysecret {
		resultCdn := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps.gaia.io/v1alpha1",
				"kind":       "cdnsuppliers",
				"spec": map[string]interface{}{
					"supplierName":         cdnProvider.SupplierName,
					"cloudAccessKeyid":     cdnProvider.CloudAccessKeyid,
					"cloudAccessKeysecret": cdnProvider.CloudAccessKeysecret,
				},
			},
		}
		_, updateCdnErr := lib.DynamicClient.Resource(cdnGvr).Namespace("gaia-frontend").Update(context.TODO(), resultCdn, metaV1.UpdateOptions{})
		if updateCdnErr != nil {
			klog.Info(updateCdnErr)
			return false
		}
	}
	return true
}
