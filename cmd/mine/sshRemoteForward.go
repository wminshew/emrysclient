package mine

import (
	"context"
	"fmt"
	"os/exec"
)

func (w *worker) sshRemoteForward(ctx context.Context, sshKeyFile string) error {
	// ssh -v -i {id_rsa} -N -R /home/{jID}/notebook.sock:127.0.0.1:8889 -p 2222 {jID}@notebook.emrys.io -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
	cmdStr := "ssh"
	// TODO: change port? what if running notebooks for two different people?
	// TODO: consider using unix domain sockets inside /home/emrys/.config/{jID} ?
	args := []string{"-q", "-i", sshKeyFile, "-N", "-R", fmt.Sprintf("/home/%s/notebook.sock:127.0.0.1:8889", w.jID), "-p", "2222", fmt.Sprintf("%s@notebook.emrys.io", w.jID), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	cmd := exec.CommandContext(ctx, cmdStr, args...)
	return cmd.Run()
}
