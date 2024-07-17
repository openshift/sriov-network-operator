package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/renameio/v2"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// Store interface provides methods to store and query information
// about OVS bridges that are managed by the operator
//
//go:generate ../../../../../../bin/mockgen -destination mock/mock_store.go -source store.go
type Store interface {
	// GetManagedOVSBridges returns map with saved information about managed OVS bridges.
	// Bridge name is a key in the map
	GetManagedOVSBridges() (map[string]*sriovnetworkv1.OVSConfigExt, error)
	// GetManagedOVSBridge returns saved information about managed OVS bridge
	GetManagedOVSBridge(name string) (*sriovnetworkv1.OVSConfigExt, error)
	// AddManagedOVSBridge save information about the OVS bridge
	AddManagedOVSBridge(br *sriovnetworkv1.OVSConfigExt) error
	// RemoveManagedOVSBridge removes saved information about the OVS bridge
	RemoveManagedOVSBridge(name string) error
}

// New returns default implementation of Store interfaces
func New() Store {
	s := &ovsStore{
		lock: &sync.RWMutex{},
	}
	return s
}

type ovsStore struct {
	lock  *sync.RWMutex
	cache map[string]sriovnetworkv1.OVSConfigExt
}

// loads data from the fs if required
func (s *ovsStore) ensureCacheIsLoaded() error {
	funcLog := log.Log
	if s.cache != nil {
		funcLog.V(2).Info("ensureCacheIsLoaded(): cache is already loaded")
		return nil
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	// check again after we got the lock to make sure that the cache was
	// not loaded by another goroutine while we was waiting for the lock
	if s.cache != nil {
		return nil
	}
	funcLog.V(2).Info("ensureCacheIsLoaded(): load store cache from the FS")
	var err error
	err = s.ensureStoreDirExist()
	if err != nil {
		funcLog.Error(err, "ensureCacheIsLoaded(): failed to create store dir")
		return err
	}
	s.cache, err = s.readStoreFile()
	if err != nil {
		funcLog.Error(err, "ensureCacheIsLoaded(): failed to read store file")
		return err
	}
	return nil
}

// GetManagedOVSBridges returns map with saved information about managed OVS bridges.
// Bridge name is a key in the map
func (s *ovsStore) GetManagedOVSBridges() (map[string]*sriovnetworkv1.OVSConfigExt, error) {
	funcLog := log.Log
	funcLog.V(1).Info("GetManagedOVSBridges(): get information about all managed OVS bridges from the store")
	if err := s.ensureCacheIsLoaded(); err != nil {
		return nil, err
	}
	s.lock.RLock()
	defer s.lock.RUnlock()
	result := make(map[string]*sriovnetworkv1.OVSConfigExt, len(s.cache))
	for k, v := range s.cache {
		result[k] = v.DeepCopy()
	}
	if funcLog.V(2).Enabled() {
		data, _ := json.Marshal(result)
		funcLog.V(2).Info("GetManagedOVSBridges()", "result", string(data))
	}
	return result, nil
}

// GetManagedOVSBridge returns saved information about managed OVS bridge
func (s *ovsStore) GetManagedOVSBridge(name string) (*sriovnetworkv1.OVSConfigExt, error) {
	funcLog := log.Log.WithValues("name", name)
	funcLog.V(1).Info("GetManagedOVSBridge(): get information about managed OVS bridge from the store")
	if err := s.ensureCacheIsLoaded(); err != nil {
		return nil, err
	}
	s.lock.RLock()
	defer s.lock.RUnlock()
	b, found := s.cache[name]
	if !found {
		funcLog.V(2).Info("GetManagedOVSBridge(): bridge info not found")
		return nil, nil
	}
	if funcLog.V(2).Enabled() {
		data, _ := json.Marshal(&b)
		funcLog.V(2).Info("GetManagedOVSBridge()", "result", string(data))
	}
	return b.DeepCopy(), nil
}

// AddManagedOVSBridge save information about the OVS bridge
func (s *ovsStore) AddManagedOVSBridge(br *sriovnetworkv1.OVSConfigExt) error {
	log.Log.V(1).Info("AddManagedOVSBridge(): add information about managed OVS bridge to the store", "name", br.Name)
	if err := s.ensureCacheIsLoaded(); err != nil {
		return err
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	revert := s.putCacheEntryToStash(br.Name)
	s.cache[br.Name] = *br.DeepCopy()
	if err := s.writeStoreFile(); err != nil {
		revert()
		return err
	}
	return nil
}

// RemoveManagedOVSBridge removes saved information about the OVS bridge
func (s *ovsStore) RemoveManagedOVSBridge(name string) error {
	log.Log.V(1).Info("RemoveManagedOVSBridge(): remove information about managed OVS bridge from the store", "name", name)
	if err := s.ensureCacheIsLoaded(); err != nil {
		return err
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	revert := s.putCacheEntryToStash(name)
	delete(s.cache, name)
	if err := s.writeStoreFile(); err != nil {
		revert()
		return err
	}
	return nil
}

// saves the current value from the cache to a temporary variable
// and returns the function which can be used to restore it in the cache.
// the caller of this function must hold the write lock for the store.
func (s *ovsStore) putCacheEntryToStash(key string) func() {
	origValue, isSet := s.cache[key]
	return func() {
		if isSet {
			s.cache[key] = origValue
		} else {
			delete(s.cache, key)
		}
	}
}

func (s *ovsStore) ensureStoreDirExist() error {
	storeDir := filepath.Dir(s.getStoreFilePath())
	_, err := os.Stat(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(storeDir, 0755)
			if err != nil {
				return fmt.Errorf("failed to create directory for store %s: %v", storeDir, err)
			}
		} else {
			return fmt.Errorf("failed to check if directory for store exist %s: %v", storeDir, err)
		}
	}
	return nil
}

func (s *ovsStore) readStoreFile() (map[string]sriovnetworkv1.OVSConfigExt, error) {
	storeFilePath := s.getStoreFilePath()
	funcLog := log.Log.WithValues("storeFilePath", storeFilePath)
	funcLog.V(2).Info("readStoreFile(): read OVS store file")
	result := map[string]sriovnetworkv1.OVSConfigExt{}
	data, err := os.ReadFile(storeFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			funcLog.V(2).Info("readStoreFile(): OVS store file not found")
			return result, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		funcLog.Error(err, "readStoreFile(): failed to unmarshal content of the OVS store file")
		return nil, err
	}
	return result, nil
}

func (s *ovsStore) writeStoreFile() error {
	storeFilePath := s.getStoreFilePath()
	funcLog := log.Log.WithValues("storeFilePath", storeFilePath)
	data, err := json.Marshal(s.cache)
	if err != nil {
		funcLog.Error(err, "writeStoreFile(): can't serialize cached info about managed OVS bridges")
		return err
	}
	if err := renameio.WriteFile(storeFilePath, data, 0644); err != nil {
		funcLog.Error(err, "writeStoreFile(): can't write info about managed OVS bridge to disk")
		return err
	}
	return nil
}

func (s *ovsStore) getStoreFilePath() string {
	return utils.GetHostExtensionPath(consts.ManagedOVSBridgesPath)
}
