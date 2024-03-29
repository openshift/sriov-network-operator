// Code generated by lister-gen. DO NOT EDIT.

package v1

import (
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

// SriovNetworkNodePolicyLister helps list SriovNetworkNodePolicies.
// All objects returned here must be treated as read-only.
type SriovNetworkNodePolicyLister interface {
	// List lists all SriovNetworkNodePolicies in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1.SriovNetworkNodePolicy, err error)
	// SriovNetworkNodePolicies returns an object that can list and get SriovNetworkNodePolicies.
	SriovNetworkNodePolicies(namespace string) SriovNetworkNodePolicyNamespaceLister
	SriovNetworkNodePolicyListerExpansion
}

// sriovNetworkNodePolicyLister implements the SriovNetworkNodePolicyLister interface.
type sriovNetworkNodePolicyLister struct {
	indexer cache.Indexer
}

// NewSriovNetworkNodePolicyLister returns a new SriovNetworkNodePolicyLister.
func NewSriovNetworkNodePolicyLister(indexer cache.Indexer) SriovNetworkNodePolicyLister {
	return &sriovNetworkNodePolicyLister{indexer: indexer}
}

// List lists all SriovNetworkNodePolicies in the indexer.
func (s *sriovNetworkNodePolicyLister) List(selector labels.Selector) (ret []*v1.SriovNetworkNodePolicy, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.SriovNetworkNodePolicy))
	})
	return ret, err
}

// SriovNetworkNodePolicies returns an object that can list and get SriovNetworkNodePolicies.
func (s *sriovNetworkNodePolicyLister) SriovNetworkNodePolicies(namespace string) SriovNetworkNodePolicyNamespaceLister {
	return sriovNetworkNodePolicyNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// SriovNetworkNodePolicyNamespaceLister helps list and get SriovNetworkNodePolicies.
// All objects returned here must be treated as read-only.
type SriovNetworkNodePolicyNamespaceLister interface {
	// List lists all SriovNetworkNodePolicies in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1.SriovNetworkNodePolicy, err error)
	// Get retrieves the SriovNetworkNodePolicy from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1.SriovNetworkNodePolicy, error)
	SriovNetworkNodePolicyNamespaceListerExpansion
}

// sriovNetworkNodePolicyNamespaceLister implements the SriovNetworkNodePolicyNamespaceLister
// interface.
type sriovNetworkNodePolicyNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all SriovNetworkNodePolicies in the indexer for a given namespace.
func (s sriovNetworkNodePolicyNamespaceLister) List(selector labels.Selector) (ret []*v1.SriovNetworkNodePolicy, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.SriovNetworkNodePolicy))
	})
	return ret, err
}

// Get retrieves the SriovNetworkNodePolicy from the indexer for a given namespace and name.
func (s sriovNetworkNodePolicyNamespaceLister) Get(name string) (*v1.SriovNetworkNodePolicy, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1.Resource("sriovnetworknodepolicy"), name)
	}
	return obj.(*v1.SriovNetworkNodePolicy), nil
}
