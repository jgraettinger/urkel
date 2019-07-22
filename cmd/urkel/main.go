package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/jessevdk/go-flags"
	"github.com/jgraettinger/urkel"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type Serve struct {
	Port uint32 `long:"port" env:"PORT" default:"1666" description:"Service address port"`
}

func (s Serve) Execute(args []string) error {
	log.WithField("config", s).Info("starting urkel server")

	var sock, err = net.Listen("tcp", ":"+strconv.Itoa(int(s.Port)))
	if err != nil {
		return err
	}
	var srv = grpc.NewServer(grpc.KeepaliveParams(keepalive.ServerParameters{Time: time.Second}))
	urkel.RegisterChaosServer(srv, new(service))
	return srv.Serve(sock)
}

func main() {
	var parser = flags.NewParser(nil, flags.Default)

	_, _ = parser.AddCommand("serve", "Serve faults",
		"Serve a Chaos service over port 1666", new(Serve))

	if _, err := parser.ParseArgs(os.Args[1:]); err != nil {
		log.WithError(err).Fatal("failed to parse arguments")
	}
}

// service implements ChaosServer.
type service struct{}

// InjectFault injects a stream of Faults specified by the client until the
// |stream| either closes or fails, at which point all injected faults are
// rolled back.
func (svc *service) InjectFault(stream urkel.Chaos_InjectFaultServer) error {
	log.Info("starting stream")

	var rollbacks []func() error
	defer unwind(&rollbacks)

	for {
		var fault, err = stream.Recv()
		if err == io.EOF {
			return unwind(&rollbacks)
		} else if err != nil {
			return err
		}

		if fault.Partition != nil {
			log.WithField("fault", fault).Info("handling partition")

			if undo, err := svc.partition(fault.Partition); err != nil {
				return err
			} else {
				rollbacks = append(rollbacks, undo)
			}
		}

		if err = stream.Send(&empty.Empty{}); err != nil {
			return err
		}
	}
}

func (svc *service) partition(partition *urkel.Partition) (func() error, error) {
	if partition.InterfaceIndex == "" {
		return nil, errors.New("InterfaceIndex not set")
	} else if partition.FromIpRange == "" {
		return nil, errors.New("FromIpRange not set")
	} else if partition.Mode == "" {
		return nil, errors.New("Mode not set")
	}

	var cmd = "iptables --source " + partition.FromIpRange + " -j " + partition.Mode

	if _, err := execInNetNS(partition.InterfaceIndex, cmd+" -A INPUT"); err != nil {
		return nil, err
	}
	return func() error {
		var _, err = execInNetNS(partition.InterfaceIndex, cmd+" -D INPUT")
		return err
	}, nil
}

// unwind executes a set of |rollbacks|, returning the first error encountered.
// |rollbacks| is modified in-place as each is popped and executed.
func unwind(rollbacks *[]func() error) error {
	var firstErr error

	for {
		var l = len(*rollbacks)

		if l == 0 {
			break
		} else if err := (*rollbacks)[l-1](); err != nil && firstErr == nil {
			firstErr = err
		}

		*rollbacks = (*rollbacks)[:l-1]
	}
	return firstErr
}

// execInNetNS executes |command| in the network namespace identified by |ifindex|,
// returning its trimmed output or error.
func execInNetNS(ifindex, command string) (string, error) {
	// Find the veth interface corresponding to the given interface index, and
	// extract its network namespace name (eg, cni-b3aa762a-305c-3979-37ab-05e5022ff6e7).
	var netns, err = execShell(`ip link show | grep -A1 "^` + ifindex + `" | grep "link-netns \K(cni-\S+)" -oP`)
	if err != nil {
		return "", err
	}
	return execShell("ip netns exec " + netns + " " + command)
}

func execShell(command string) (string, error) {
	var stdout, stderr bytes.Buffer

	log.WithField("cmd", command).Info("execShell")

	var cmd = exec.Command("/bin/sh", "-c", command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var err = cmd.Run()

	if stderr.Len() != 0 {
		return "", errors.New(stderr.String())
	} else if err != nil {
		return "", err
	} else {
		return strings.TrimSpace(stdout.String()), nil
	}
}
