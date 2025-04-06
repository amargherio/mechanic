package consts

const IMDS_SCHEDULED_EVENTS_API_ENDPOINT = "http://169.254.169.254/metadata/scheduledevents"

type NodeCondition string

const (
	Freeze                      NodeCondition = "FreezeScheduled"
	Reboot                      NodeCondition = "RebootScheduled"
	Redeploy                    NodeCondition = "RedeployScheduled"
	Preempt                     NodeCondition = "PreemptScheduled"
	Terminate                   NodeCondition = "TerminateScheduled"
	VMEvent                     NodeCondition = "VMEventScheduled"
	KubeletProblem              NodeCondition = "KubeletProblem"
	KernelDeadlock              NodeCondition = "KernelDeadlock"
	FrequentKubeletRestart      NodeCondition = "FrequentKubeletRestart"
	FrequentContainerdRestart   NodeCondition = "FrequentContainerdRestart"
	FileSystemCorruptionProblem NodeCondition = "FileSystemCorruptionProblem"
)
