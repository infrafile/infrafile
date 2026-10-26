package main

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func startRunProcess(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	kill := func() { once.Do(func() { _ = windows.CloseHandle(job) }) }
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		kill()
		return nil, err
	}
	// Assign the job before user code can run and spawn untracked descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		kill()
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err == nil {
		err = resumeRunProcess(uint32(cmd.Process.Pid))
	}
	if err != nil {
		_ = cmd.Process.Kill()
		kill()
		_ = cmd.Wait()
		return nil, err
	}
	return kill, nil
}

func resumeRunProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return errors.New("子进程主线程不存在")
	}
	return err
}
