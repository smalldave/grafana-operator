package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tsloughter/grafana-operator/pkg/grafana"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// ConfigMapController watches the kubernetes api for changes to configmaps and
// creates a RoleBinding for that particular configmap.
type ConfigMapController struct {
	configmapInformer cache.SharedIndexInformer
	kclient           *kubernetes.Clientset
	g                 *grafana.DashboardsClient
}

type DashboardConfigMap struct {
	Dashboard *Dashboard `json:"dashboard"`
}

type Dashboard struct {
	Uid string `json:"uid"`
}

// Run starts the process for listening for configmap changes and acting upon those changes.
func (c *ConfigMapController) Run(stopCh <-chan struct{}, wg *sync.WaitGroup) {
	// When this function completes, mark the go function as done
	defer wg.Done()

	// Increment wait group as we're about to execute a go function
	wg.Add(1)

	// Execute go function
	go c.configmapInformer.Run(stopCh)

	// Wait till we receive a stop signal
	<-stopCh
}

// NewConfigMapController creates a new NewConfigMapController
func NewConfigMapController(kclient *kubernetes.Clientset, g *grafana.DashboardsClient) *ConfigMapController {
	configmapWatcher := &ConfigMapController{}

	// Create informer for watching ConfigMaps
	configmapInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return kclient.Core().ConfigMaps(metav1.NamespaceAll).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return kclient.Core().ConfigMaps(metav1.NamespaceAll).Watch(options)
			},
		},
		&v1.ConfigMap{},
		3*time.Minute,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	configmapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    configmapWatcher.createDashboards,
		UpdateFunc: configmapWatcher.updateDashboards,
		DeleteFunc: configmapWatcher.deleteDashboards,
	})

	configmapWatcher.kclient = kclient
	configmapWatcher.configmapInformer = configmapInformer
	configmapWatcher.g = g

	return configmapWatcher
}

func (c *ConfigMapController) createDashboards(obj interface{}) {
	c.syncDashboards(obj, "create", func(uid string, json string) error { return c.g.Create(strings.NewReader(json)) })
}

func (c *ConfigMapController) updateDashboards(oldObj, newObj interface{}) {
	// PENDING: Could check uid is the same
	c.syncDashboards(newObj, "update", func(uid string, json string) error { return c.g.Create(strings.NewReader(json)) })
}

func (c *ConfigMapController) deleteDashboards(obj interface{}) {
	c.syncDashboards(obj, "delete", func(uid string, json string) error { return c.g.Delete(uid) })
}

func (c *ConfigMapController) syncDashboards(obj interface{}, method string, action func(string, string) error) {
	configmapObj := obj.(*v1.ConfigMap)
	isGrafanaDashboards, _ := configmapObj.Annotations["grafana.net/dashboards"]

	if b, err := strconv.ParseBool(isGrafanaDashboards); err == nil && b == true {
		for k, v := range configmapObj.Data {
			var dashboardConfigMap DashboardConfigMap
			err = json.Unmarshal([]byte(v), &dashboardConfigMap)
			if err != nil {
				log.Println(fmt.Sprintf("Failed to unmarshal dashboard config; %s", err.Error()))
				continue
			}
			uid := dashboardConfigMap.Dashboard.Uid
			if uid == "" {
				log.Println(fmt.Sprintf("Ignoring dashbard %s with no uid", k))
				continue
			}
			err = action(uid, v)
			if err != nil {
				log.Println(fmt.Sprintf("Failed to %s dashboard; %s", method, err.Error()))
				continue
			}
			log.Println(fmt.Sprintf("Successfully %sd dashboard: %s", method, k))
		}
	} else {
		log.Println(fmt.Sprintf("Skipping configmap: %s", configmapObj.Name))
	}
}
