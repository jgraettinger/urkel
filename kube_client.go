//go:generate protoc -I . --gogo_out=plugins=grpc:. urkel.proto

package urkel

import (
	crand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"sync"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// PartitionMode to use with `iptables` command; REJECT or DROP.
type PartitionMode string

var (
	Drop   PartitionMode = "DROP"
	Reject PartitionMode = "REJECT"
)

// FetchPods from Kubernetes using the given |namespace| and ListOptions.
// Pods are returned in randomly shuffled order.
func FetchPods(t require.TestingT, namespace, selector string) []v1.Pod {
	var pods, err = kubeClient(t).CoreV1().Pods(namespace).List(metav1.ListOptions{LabelSelector: selector})
	assert.NoError(t, err, "listing pods")
	assert.NotEmpty(t, pods.Items)
	return shuffled(pods.Items)
}

// kubeConfig builds and returns a K8s cluster config.
func kubeConfig(t require.TestingT) *restclient.Config {
	kubeConfigOnce.Do(func() {
		var cfg = os.Getenv("KUBECONFIG")
		if cfg == "" {
			if cfg = os.Getenv("HOME"); cfg == "" {
				cfg = os.Getenv("USERPROFILE")
			}
			cfg = filepath.Join(cfg, ".kube", "config")

			if _, err := os.Stat(cfg); err != nil {
				cfg = "" // Fall back to in-cluster config mode.
			}
		}

		var err error
		kubeConfigInstance, err = clientcmd.BuildConfigFromFlags("", cfg)
		require.NoError(t, err, "building kube config")
	})
	return kubeConfigInstance
}

// kubeClient builds and returns a K8s cluster client.
func kubeClient(t require.TestingT) *kubernetes.Clientset {
	kubeClientOnce.Do(func() {
		var err error
		kubeClientInstance, err = kubernetes.NewForConfig(kubeConfig(t))
		require.NoError(t, err, "building kube client")
	})
	return kubeClientInstance
}

// shuffled shuffles |pods| order, and returns it.
func shuffled(pods []v1.Pod) []v1.Pod {
	rand.Shuffle(len(pods), func(i, j int) { pods[i], pods[j] = pods[j], pods[i] })
	return pods
}

func init() {
	// Seed prng with a cryptographic random number.
	var b [8]byte
	if _, err := crand.Reader.Read(b[:]); err != nil {
		panic(err)
	}
	rand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
}

var (
	faultConns  = make(map[string]*grpc.ClientConn)
	faultConnMu sync.Mutex

	kubeConfigOnce     sync.Once
	kubeConfigInstance *restclient.Config

	kubeClientInstance *kubernetes.Clientset
	kubeClientOnce     sync.Once
)
