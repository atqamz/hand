package boot

func ID() string { return FromFile("/proc/sys/kernel/random/boot_id") }
