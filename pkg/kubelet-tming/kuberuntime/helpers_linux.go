package kuberuntime

const (
	minShares	= 2
	sharesPerCPU 	= 1024
	milliCPUToCPU   = 1000

	// 100000 is equivalent to 100ms
	quotaPeriod 	= 100000
	minQuotaPeriod  = 1000
)

func milliCPUToShares(milliCPU int64) int64 {
	if milliCPU == 0 {
		// Return 2 here to really match kernel default for zero milliCPU
		return minShares
	}

	// Conceptually (milliCPU / milliCPUToCPU) * sharesPerCPU
	shares := (milliCPU * sharesPerCPU) / milliCPUToCPU

	if shares < minShares {
		return minShares
	}

	return shares
}

// milliCPUToQuota converts milliCPU to CFS quota and period values
func milliCPUToQuota(milliCPU int64, period int64) (quota int64) {
	// CFS quota is measured in two values:
	//  - cfs_period_us=100ms (the amount of time to measure usage across)
	//  - cfs_quota=20ms (the amount of cpu time allowed to be used across a period)
	// so in the above example, you are limited to 20% of a single CPU
	// for multi-cpu environments, you just scale equivalent amounts
	// see https://www.kernel.org/doc/Documentation/scheduler/sched-bwc.txt for details
	if milliCPU == 0 {
		return
	}

	// we then convert your milliCPU to a value normalized over a period
	quota = (milliCPU * period) / milliCPUToCPU

	// quota needs to be a minimum of 1ms.
	if quota < minQuotaPeriod {
		quota = minQuotaPeriod
	}

	return
}
