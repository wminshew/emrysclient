package worker

import (
	"context"
	"fmt"
	"os/exec"
)

func (w *Worker) sshRemoteForward(ctx context.Context, sshKeyFile string) *exec.Cmd {
	// ssh -v -i {id_rsa} -N -R /home/{jID}/notebook.sock:127.0.0.1:{port} -p 2222 {jID}@notebook.emrys.io -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
	cmdStr := "ssh"
	args := []string{"-q", "-i", sshKeyFile, "-N", "-R", fmt.Sprintf("/home/%s/notebook.sock:127.0.0.1:%s", w.JobID, w.Port), "-p", "2222", fmt.Sprintf("%s@notebook.emrys.io", w.JobID), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	return exec.CommandContext(ctx, cmdStr, args...)
}
