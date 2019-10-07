package urkel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// FaultSet dispatches a set of removable faults to apply to a collection of Pods.
type FaultSet struct {
	t       require.TestingT
	streams map[string]Chaos_InjectFaultClient
}

// NewFaultSet returns an empty FaultSet.
func NewFaultSet(t require.TestingT) *FaultSet {
	return &FaultSet{t: t, streams: make(map[string]Chaos_InjectFaultClient)}
}

// Partition Pods in |partA| from Pods in |partB|. The PartitionMode determines
// whether connections are actively reset (REJECT) or packets are passively
// ignored (DROP).
func (fs *FaultSet) Partition(partA, partB []v1.Pod, mode PartitionMode) {
	fmt.Println("partition ", podNames(partA), podNames(partB), mode)

	// Determine the iflink index of each pod.
	var ind = make(map[string]string)
	for _, p := range partA {
		ind[p.Name] = execRemote(fs.t, &p, "cat", "/sys/class/net/eth0/iflink")
	}
	for _, p := range partB {
		ind[p.Name] = execRemote(fs.t, &p, "cat", "/sys/class/net/eth0/iflink")
	}

	// Dispatch bi-directional Partition faults for each pairing. The bi-
	// directionality is needed to properly partition w.r.t the K8s service
	// address, as a unilateral partition would still allow flow to a
	// partitioned pod which happens to traverse over a service address IP.
	for _, a := range partA {
		var ifIndex = ind[a.Name]

		for _, b := range partB {
			fs.install(a, Fault{
				Partition: &Partition{
					InterfaceIndex: ifIndex,
					FromIpRange:    b.Status.PodIP,
					Mode:           string(mode),
				},
			})
		}
	}
	for _, b := range partB {
		var ifIndex = ind[b.Name]

		for _, a := range partA {
			fs.install(b, Fault{
				Partition: &Partition{
					InterfaceIndex: ifIndex,
					FromIpRange:    a.Status.PodIP,
					Mode:           string(mode),
				},
			})
		}
	}
}

// TODO(johnny): Support for CPU & disk stress; filled disks; packet latency and jitter.

// Crash Pods immediately, with no grace period. Crash faults are not remove-able.
func (fs *FaultSet) Crash(pods ...v1.Pod) {
	fmt.Println("crash ", podNames(pods))

	var zero = new(int64)
	var coreV1 = kubeClient(fs.t).CoreV1()

	for _, p := range pods {
		assert.NoError(fs.t, coreV1.Pods(p.Namespace).Delete(p.Name, &metav1.DeleteOptions{
			GracePeriodSeconds: zero,
		}))
	}
}

// Delete pods using their default GracePeriodSeconds policy. Deletion faults are not remove-able.
func (fs *FaultSet) Delete(pods ...v1.Pod) {
	fmt.Println("delete ", podNames(pods))

	var coreV1 = kubeClient(fs.t).CoreV1()

	for _, p := range pods {
		assert.NoError(fs.t, coreV1.Pods(p.Namespace).Delete(p.Name, &metav1.DeleteOptions{}))
	}
}

// RemoveAll previously installed faults.
func (fs *FaultSet) RemoveAll() {
	for _, s := range fs.streams {
		assert.NoError(fs.t, s.CloseSend())

		var _, err = s.Recv()
		assert.Equal(fs.t, err, io.EOF)
	}
}

// install a fault of the |pod| via a daemon running on the Pod's host.
func (fs *FaultSet) install(pod v1.Pod, fault Fault) {
	var addr = pod.Status.HostIP + ":1666"
	var err error

	var s, ok = fs.streams[addr]
	if !ok {

		faultConnMu.Lock()
		var conn, ok = faultConns[addr]
		faultConnMu.Unlock()

		if !ok {
			conn, err = grpc.Dial(addr, grpc.WithInsecure())
			require.NoError(fs.t, err)

			faultConnMu.Lock()
			faultConns[addr] = conn
			faultConnMu.Unlock()
		}

		s, err = NewChaosClient(conn).InjectFault(context.Background())
		require.NoError(fs.t, err, "starting fault stream")

		fs.streams[addr] = s
	}

	assert.NoError(fs.t, s.Send(&fault))

	_, err = s.Recv() // Read confirmation.
	assert.NoError(fs.t, err)
}

// execRemote command |args| on the given |pod|.
func execRemote(t require.TestingT, pod *v1.Pod, args ...string) string {
	var stdout, stderr bytes.Buffer

	req := kubeClient(t).CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")

	req.VersionedParams(&v1.PodExecOptions{
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
		Container: pod.Spec.Containers[0].Name,
		Command:   args,
	}, scheme.ParameterCodec)

	rc, err := remotecommand.NewSPDYExecutor(kubeConfig(t), "POST", req.URL())
	assert.NoError(t, err, "starting exec stream")

	err = rc.Stream(remotecommand.StreamOptions{
		Stdin:             nil,
		Stdout:            &stdout,
		Stderr:            &stderr,
		Tty:               false,
		TerminalSizeQueue: nil,
	})
	assert.NoError(t, err, "reading exec stream")
	assert.Empty(t, stderr.String())

	return strings.TrimSpace(stdout.String())
}

func podNames(pods []v1.Pod) []string {
	var out []string

	for _, p := range pods {
		out = append(out, p.Name)
	}
	return out
}
