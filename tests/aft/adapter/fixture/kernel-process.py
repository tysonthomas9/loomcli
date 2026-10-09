"""Fixed private process-generation protocol. No process enumeration or shell.

Linux holds a pidfd; Darwin holds a task port and process-exit kqueue subscription.
Both signal the captured kernel object, never a subsequently reused PID. Native calls
are exercised only by a later authorized runtime gate.
Darwin ABI: apple-oss-distributions/xnu/bsd/sys/proc_info.h, PROC_PIDTBSDINFO.
"""
import ctypes
import hashlib
import json
import os
import select
import signal
import struct
import sys


def arguments_darwin(pid):
    libc = ctypes.CDLL('/usr/lib/libSystem.B.dylib', use_errno=True)
    mib = (ctypes.c_int * 3)(1, 49, pid)
    size = ctypes.c_size_t(1024 * 1024)
    data = ctypes.create_string_buffer(size.value)
    if libc.sysctl(mib, 3, data, ctypes.byref(size), None, 0) != 0:
        raise RuntimeError('identity unavailable')
    raw = data.raw[:size.value]
    argc = struct.unpack_from('i', raw)[0]
    if argc < 1 or argc > 4096:
        raise RuntimeError('identity incomplete')
    offset = raw.index(b'\0', 4) + 1
    while offset < len(raw) and raw[offset] == 0:
        offset += 1
    values = raw[offset:].split(b'\0')
    argv = values[:argc]
    env = dict(value.split(b'=', 1) for value in values[argc:] if b'=' in value)
    return b'\0'.join(argv) + b'\0', env


def identity(pid):
    if sys.platform == 'linux':
        with open(f'/proc/{pid}/stat', 'rb') as file:
            fields = file.read(65536).rsplit(b') ', 1)[1].split()
        with open('/proc/sys/kernel/random/boot_id') as file:
            boot = file.read(64).strip()
        with open(f'/proc/{pid}/cmdline', 'rb') as file:
            argv = file.read(1024 * 1024 + 1)
        with open(f'/proc/{pid}/environ', 'rb') as file:
            raw_env = file.read(1024 * 1024 + 1)
        if len(argv) > 1024 * 1024 or len(raw_env) > 1024 * 1024:
            raise RuntimeError('identity incomplete')
        env = dict(value.split(b'=', 1) for value in raw_env.split(b'\0') if b'=' in value)
        executable = os.readlink(f'/proc/{pid}/exe')
        generation = f'{pid}:{boot}:{fields[19].decode()}'
        parent = int(fields[1])
    elif sys.platform == 'darwin':
        lib = ctypes.CDLL('/usr/lib/libproc.dylib', use_errno=True)
        data = ctypes.create_string_buffer(136)
        if lib.proc_pidinfo(pid, 3, ctypes.c_uint64(0), data, 136) != 136:
            raise RuntimeError('identity unavailable')
        sec, usec = struct.unpack_from('QQ', data.raw, 120)
        observed_pid, parent = struct.unpack_from('II', data.raw, 12)
        if observed_pid != pid:
            raise RuntimeError('identity changed')
        executable_buffer = ctypes.create_string_buffer(4096)
        if lib.proc_pidpath(pid, executable_buffer, 4096) <= 0:
            raise RuntimeError('identity unavailable')
        executable = os.fsdecode(executable_buffer.value)
        argv, env = arguments_darwin(pid)
        generation = f'{pid}:{sec}:{usec}'
    else:
        raise RuntimeError('platform unsupported')
    return {'pid': pid, 'generation': generation, 'executable': os.path.realpath(executable),
            'argvSha256': hashlib.sha256(argv).hexdigest(), 'parentPid': parent,
            'configurationRoot': os.fsdecode(env.get(b'LOOM_CONFIG_DIR', b''))}


class RegisteredProcess:
    def __init__(self, pid):
        self.pid = pid
        self.before = identity(pid)
        self.exited = False
        self.fd = None
        self.queue = None
        self.task = None
        self.mach = None
        if sys.platform == 'linux':
            self.fd = os.pidfd_open(pid)
        else:
            self.queue = select.kqueue()
            self.queue.control([select.kevent(pid, filter=select.KQ_FILTER_PROC,
                                             flags=select.KQ_EV_ADD | select.KQ_EV_ONESHOT,
                                             fflags=select.KQ_NOTE_EXIT)], 0, 0)
            self.mach = ctypes.CDLL('/usr/lib/libSystem.B.dylib', use_errno=True)
            self.self_port = ctypes.c_uint.in_dll(self.mach, 'mach_task_self_').value
            self.task = ctypes.c_uint(0)
            if self.mach.task_for_pid(self.self_port, pid, ctypes.byref(self.task)) != 0:
                self.close()
                raise RuntimeError('kernel ownership unavailable')
        if self.same(identity(pid)) is False:
            self.close()
            raise RuntimeError('identity changed')

    def same(self, current):
        return all(current[key] == self.before[key] for key in
                   ('pid', 'generation', 'executable', 'argvSha256', 'configurationRoot'))

    def inspect(self):
        if not self.exited:
            if self.fd is not None:
                self.exited = bool(select.select([self.fd], [], [], 0)[0])
            else:
                self.exited = bool(self.queue.control(None, 1, 0))
            if not self.exited and not self.same(identity(self.pid)):
                raise RuntimeError('identity changed')
        return {**self.before, 'state': 'exited' if self.exited else 'running'}

    def stop(self):
        if self.inspect()['state'] == 'running':
            if self.fd is not None:
                signal.pidfd_send_signal(self.fd, signal.SIGKILL)
                self.exited = bool(select.select([self.fd], [], [], 15)[0])
            else:
                if not self.same(identity(self.pid)):
                    raise RuntimeError('identity changed')
                # task_terminate targets the retained task object. If permissions
                # deny task_for_pid, acquisition fails; there is no PID fallback.
                result = self.mach.task_terminate(self.task.value)
                if result != 0 and self.inspect()['state'] != 'exited':
                    raise RuntimeError('cleanup unavailable')
                # inspect above may consume the one-shot exit notification.
                # Terminal state is monotonic; never read it away on retry.
                if not self.exited:
                    self.exited = bool(self.queue.control(None, 1, 15))
        if not self.exited:
            raise RuntimeError('cleanup incomplete')
        return self.inspect()

    def terminate_gracefully(self):
        # The source restart actor is fixed SIGTERM. It never uses force cleanup
        # or an adapter-spawned replacement. Only the captured Linux pidfd can
        # implement it here; Darwin has no ownership-safe TERM port yet.
        if self.fd is None:
            raise NotImplementedError('graceful termination unsupported')
        if self.inspect()['state'] == 'running':
            signal.pidfd_send_signal(self.fd, signal.SIGTERM)
            if not self.exited:
                self.exited = bool(select.select([self.fd], [], [], 15)[0])
        if not self.exited:
            raise RuntimeError('graceful termination incomplete')
        return self.inspect()

    def close(self):
        if self.task is not None and self.task.value:
            self.mach.mach_port_deallocate(self.self_port, self.task.value)
            self.task = None
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None
        if self.queue is not None:
            self.queue.close()
            self.queue = None


def main():
    if len(sys.argv) != 2 or not sys.argv[1].isdigit() or int(sys.argv[1]) <= 0:
        raise RuntimeError('invalid registration')
    owned = RegisteredProcess(int(sys.argv[1]))
    try:
        print(json.dumps(owned.inspect()), flush=True)
        for line in sys.stdin:
            if len(line) > 128:
                raise RuntimeError('invalid command')
            request = json.loads(line)
            if request in ({'operation': 'inspect'}, {'operation': 'stop'}, {'operation': 'terminate-gracefully'}):
                try:
                    operation = request['operation']
                    result = owned.inspect() if operation == 'inspect' else owned.stop() if operation == 'stop' else owned.terminate_gracefully()
                except NotImplementedError:
                    result = {'error': 'unsupported-capability'}
                except Exception:
                    # Preserve the same kernel object for an exact cleanup retry.
                    result = {'error': 'cleanup-unverified'}
            elif request == {'operation': 'abandon'}:
                return
            elif request == {'operation': 'close'} and owned.inspect()['state'] == 'exited':
                return
            else:
                raise RuntimeError('invalid command')
            print(json.dumps(result), flush=True)
    finally:
        owned.close()


if __name__ == '__main__':
    try:
        main()
    except Exception:
        sys.stderr.write('Owned process protocol failed\n')
        sys.exit(1)
