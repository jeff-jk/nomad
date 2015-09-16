// Package exec is used to invoke child processes across various platforms to
// provide the following features:
//
// - Least privilege
// - Resource constraints
// - Process isolation
//
// A "platform" may be defined as coarsely as "Windows" or as specifically as
// "linux 3.20 with systemd". This allows Nomad to use best-effort, best-
// available capabilities of each platform to provide resource constraints,
// process isolation, and security features, or otherwise take advantage of
// features that are unique to that platform.
//
// The semantics of any particular instance are left up to the implementation.
// However, these should be completely transparent to the calling context. In
// other words, the Java driver should be able to call exec for any platform and
// just work.
package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/hashicorp/nomad/nomad/structs"
)

// Executor is an interface that any platform- or capability-specific exec
// wrapper must implement. You should not need to implement a Java executor.
// Rather, you would implement a cgroups executor that the Java driver will use.
type Executor interface {
	// Available should return true or false based on whether the current platform
	// can run this type of executor, based on capability testing. Returning
	// true does not guarantee that this executor will be used.
	Available() bool

	// Limit must be called before Start and restricts the amount of resources
	// the process can use. Note that an error may be returned ONLY IF the
	// executor implements resource limiting. Otherwise Limit is ignored.
	Limit(*structs.Resources) error

	// RunAs sets the user we should use to run this command. This may be set as
	// a username, uid, or other identifier. The implementation will decide what
	// to do with it, if anything. Note that an error may be returned ONLY IF
	// the executor implements user lookups. Otherwise RunAs is ignored.
	RunAs(string) error

	// Start the process. This may wrap the actual process in another command,
	// depending on the capabilities in this environment. Errors that arise from
	// Limits or Runas will bubble through Start()
	Start() error

	// Open should be called to restore a previous pid. This might be needed if
	// nomad is restarted. This sets os.Process internally.
	Open(int) error

	// This is a convenience wrapper around Command().Wait()
	Wait() error

	// This is a convenience wrapper around Command().Process.Pid
	Pid() (int, error)

	// Shutdown should use a graceful stop mechanism so the application can
	// perform checkpointing or cleanup, if such a mechanism is available.
	// If such a mechanism is not available, Shutdown() should call ForceStop().
	Shutdown() error

	// ForceStop will terminate the process without waiting for cleanup. Every
	// implementations must provide this.
	ForceStop() error

	// Access the underlying Cmd struct. This should never be nil. Also, this is
	// not intended to be access outside the exec package, so YMMV.
	Command() *cmd
}

// Cmd is an extension of exec.Cmd that incorporates functionality for
// re-attaching to processes, dropping priviledges, etc., based on platform-
// specific implementations.
type cmd struct {
	exec.Cmd

	// Resources is used to limit CPU and RAM used by the process, by way of
	// cgroups or a similar mechanism.
	Resources structs.Resources

	// RunAs may be a username or Uid. The implementation will decide how to use it.
	RunAs string
}

// Command is a mirror of exec.Command that returns a platform-specific Executor
func Command(name string, arg ...string) Executor {
	executor := Default()
	cmd := executor.Command()
	cmd.Path = name
	cmd.Args = append([]string{name}, arg...)

	if filepath.Base(name) == name {
		if lp, err := exec.LookPath(name); err != nil {
			// cmd.lookPathErr = err
		} else {
			cmd.Path = lp
		}
	}
	return executor
}

func OpenPid(pid int) (Executor, error) {
	executor := Default()
	err := executor.Open(pid)
	if err != nil {
		return nil, err
	}
	return executor, nil
}

// ExecutorFactory is an interface for a function that returns an Executor. This
// allows us to create Executors dynamically.
type ExecutorFactory func() Executor

var executors []ExecutorFactory
var execFactoryMutex sync.Mutex

// Register an ExecutorFactory so we can create it with Default()
func Register(executor ExecutorFactory) {
	execFactoryMutex.Lock()
	if executors == nil {
		executors = []ExecutorFactory{}
	}
	executors = append(executors, executor)
	execFactoryMutex.Unlock()
}

// Default uses capability testing to give you the best available
// executor based on your platform and execution environment. If you need a
// specific executor, call it directly.
//
// This is a simplistic strategy pattern. We can potentially improve this by
// using a decorator pattern instead.
func Default() Executor {
	// These will be IN ORDER and the first available will be used, so preferred
	// ones should be at the top and fallbacks at the bottom. Note that if these
	// are added via init() calls then the order may be a be a bit mysterious
	// even though it should be deterministic.
	// TODO Make order more explicit
	for _, factory := range executors {
		executor := factory()
		if executor.Available() {
			return executor
		}
	}

	// Always return something, even if we don't have advanced capabilities.
	return &UniversalExecutor{}
}

// UniversalExecutor should work everywhere, and as a result does not include
// any resource restrictions or runas capabilities.
type UniversalExecutor struct {
	cmd
}

func (e *UniversalExecutor) Available() bool {
	return true
}

func (e *UniversalExecutor) Limit(resources *structs.Resources) error {
	// No-op
	return nil
}

func (e *UniversalExecutor) RunAs(userid string) error {
	// No-op
	return nil
}

func (e *UniversalExecutor) Start() error {
	// We don't want to call ourself. We want to call Start on our embedded Cmd
	return e.cmd.Start()
}

func (e *UniversalExecutor) Open(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("Failed to reopen pid %d: %s", pid, err)
	}
	e.Process = process
	return nil
}

func (e *UniversalExecutor) Wait() error {
	// We don't want to call ourself. We want to call Start on our embedded Cmd
	return e.cmd.Wait()
}

func (e *UniversalExecutor) Pid() (int, error) {
	if e.cmd.Process != nil {
		return e.cmd.Process.Pid, nil
	} else {
		return 0, fmt.Errorf("Process has finished or was never started")
	}
}

func (e *UniversalExecutor) Shutdown() error {
	return e.ForceStop()
}

func (e *UniversalExecutor) ForceStop() error {
	return e.Process.Kill()
}

func (e *UniversalExecutor) Command() *cmd {
	return &e.cmd
}
