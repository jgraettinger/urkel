# Urkel

Urkel is a gRPC service and client for the injection of controlled infrastructure
failures within a Kubernetes envrionment.

An `urkel` server runs as a priviledged DaemonSet across all Kubernetes nodes.
Or, if running MicroK8s, running `urkel` on the host outside of Kubernetes is
sufficient. `urkel` uses its priviledge to run commands within the networking
namespace of Pod containers running on that same node.

Faults are initiated on behalf of a client gRPC stream which specifies faults to
install, and are kept alive so long as the gRPC client stream is still active,
after which faults are fully unwound. This design gives clients precise control
over a set of faults applied across an entire Kubernetes cluster, while ensuring
faults are removed when clients exit (or crash). This model is well suited for
crafting "chaos" tests implemented as, for example, regular test cases of the
`go test` tool.

On the client side, the `urkel` package performs configuration of a Kubernetes
client, provides helpers to identify sets of Pods to place under test, and
includes `urkel.FaultSet` for dispatching faults to appropriate `urkel` servers
within the cluster.

# Example

By way of example, this Go test partitions members of an Etcd cluster in half,
leaves the partition in place for a minute, and then exits.

```
func TestPartitionWithinEtcdCluster(t *testing.T) {
	var pods = urkel.FetchPods(t, myEtcdPodSelector)

	var fs = urkel.NewFaultSet(t)
	defer fs.RemoveAll()

	fs.Partition(pods[:len(pods)/2], pods[len(pods)/2:], drop)
	time.Sleep(time.Minute)
}
```
