package job

import (
	"context"
	"fmt"
	"os/exec"
)

// SSHLocalForward forwards local requests to the server via ssh
func (j *Job) SSHLocalForward(ctx context.Context, sshKeyFile string) *exec.Cmd {
	// ssh -v -i {id_rsa} -N -L 8888:/home/{jID}/notebook.sock -p 2222 {jID}@notebook.emrys.io -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
	cmdStr := "ssh"
	// TODO: make local port settable by user
	args := []string{"-q", "-i", sshKeyFile, "-N", "-L", fmt.Sprintf("8888:/home/%s/notebook.sock", j.ID), "-p", "2222", fmt.Sprintf("%s@notebook.emrys.io", j.ID), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	return exec.CommandContext(ctx, cmdStr, args...)
}
