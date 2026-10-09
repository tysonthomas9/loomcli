"""Pure fixed-kernel-port tests; no OS calls, process launch or helper main."""
import importlib.util
import pathlib
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    'fixture_kernel', pathlib.Path(__file__).with_name('kernel-process.py'))
kernel = importlib.util.module_from_spec(spec)
spec.loader.exec_module(kernel)


class Queue:
    def __init__(self, results):
        self.results = iter(results)
        self.reads = 0

    def control(self, *_args):
        self.reads += 1
        return next(self.results)


def registered(results, status):
    owned = kernel.RegisteredProcess.__new__(kernel.RegisteredProcess)
    owned.pid = 501
    owned.before = dict(pid=501, generation='known-start', executable='/owned/native',
                        argvSha256='a' * 64, configurationRoot='/owned/config')
    owned.exited = False
    owned.fd = None
    owned.task = types.SimpleNamespace(value=7)
    owned.queue = Queue(results)
    calls = []
    owned.mach = types.SimpleNamespace(
        task_terminate=lambda task: calls.append(task) or status)
    return owned, calls


class DarwinExitTests(unittest.TestCase):
    def test_failed_terminate_and_concurrent_exit_consumes_notification_once(self):
        owned, calls = registered([[], ['exit'], []], 5)
        with patch.object(kernel, 'identity', return_value=owned.before) as identity:
            self.assertEqual(owned.stop()['state'], 'exited')
            self.assertTrue(owned.exited)
            self.assertEqual(owned.queue.reads, 2)
            identity.reset_mock()
            self.assertEqual(owned.stop()['state'], 'exited')
            identity.assert_not_called()
        self.assertEqual(calls, [7])

    def test_successful_terminate_awaits_exit_once(self):
        owned, calls = registered([[], ['exit']], 0)
        with patch.object(kernel, 'identity', return_value=owned.before):
            self.assertEqual(owned.stop()['state'], 'exited')
            self.assertEqual(owned.inspect()['state'], 'exited')
        self.assertEqual(owned.queue.reads, 2)
        self.assertEqual(calls, [7])

    def test_missing_exit_is_incomplete_and_retry_retains_same_task(self):
        owned, calls = registered([[], [], [], ['exit']], 0)
        with patch.object(kernel, 'identity', return_value=owned.before):
            with self.assertRaisesRegex(RuntimeError, 'cleanup incomplete'):
                owned.stop()
            self.assertFalse(owned.exited)
            self.assertEqual(owned.task.value, 7)
            self.assertEqual(owned.stop()['state'], 'exited')
        self.assertEqual(calls, [7, 7])
        self.assertEqual(owned.queue.reads, 4)

    def test_failed_terminate_without_exit_does_not_await_or_claim_completion(self):
        owned, calls = registered([[], []], 5)
        with patch.object(kernel, 'identity', return_value=owned.before):
            with self.assertRaisesRegex(RuntimeError, 'cleanup unavailable'):
                owned.stop()
        self.assertFalse(owned.exited)
        self.assertEqual(owned.queue.reads, 2)
        self.assertEqual(calls, [7])


class GracefulTerminationTests(unittest.TestCase):
    def test_linux_fixed_term_uses_captured_pidfd_and_observed_exit(self):
        owned, calls = registered([], 0)
        owned.fd = 42
        with patch.object(kernel, 'identity', return_value=owned.before), \
                patch.object(kernel.signal, 'pidfd_send_signal', create=True) as send, \
                patch.object(kernel.select, 'select', side_effect=[([], [], []), ([42], [], [])]):
            self.assertEqual(owned.terminate_gracefully()['state'], 'exited')
            send.assert_called_once_with(42, kernel.signal.SIGTERM)
            self.assertTrue(owned.exited)
        self.assertEqual(calls, [])

    def test_linux_missing_exit_is_incomplete_and_same_handle_can_observe_later_exit(self):
        owned, calls = registered([], 0)
        owned.fd = 42
        with patch.object(kernel, 'identity', return_value=owned.before), \
                patch.object(kernel.signal, 'pidfd_send_signal', create=True) as send, \
                patch.object(kernel.select, 'select', side_effect=[([], [], []), ([], [], []), ([42], [], [])]):
            with self.assertRaisesRegex(RuntimeError, 'graceful termination incomplete'):
                owned.terminate_gracefully()
            self.assertFalse(owned.exited)
            self.assertEqual(owned.terminate_gracefully()['state'], 'exited')
            send.assert_called_once_with(42, kernel.signal.SIGTERM)
        self.assertEqual(calls, [])

    def test_darwin_refuses_before_any_force_or_pid_fallback(self):
        owned, calls = registered([], 0)
        with patch.object(kernel.signal, 'pidfd_send_signal', create=True) as send:
            with self.assertRaises(NotImplementedError):
                owned.terminate_gracefully()
            send.assert_not_called()
        self.assertEqual(calls, [])
        self.assertEqual(owned.queue.reads, 0)


class ExitObservationTests(unittest.TestCase):
    def test_linux_awaits_retained_pidfd_without_sending_signal(self):
        owned, calls = registered([], 0)
        owned.fd = 42
        with patch.object(kernel, 'identity', return_value=owned.before), \
                patch.object(kernel.signal, 'pidfd_send_signal', create=True) as send, \
                patch.object(kernel.select, 'select', side_effect=[([], [], []), ([42], [], [])]) as select:
            self.assertEqual(owned.await_exit()['state'], 'exited')
            self.assertEqual(select.call_args_list[-1].args, ([42], [], [], 15))
            send.assert_not_called()
        self.assertEqual(calls, [])

    def test_darwin_exit_consumed_once_survives_retry_without_identity_lookup(self):
        owned, calls = registered([[], ['exit'], []], 0)
        with patch.object(kernel, 'identity', return_value=owned.before) as identity:
            self.assertEqual(owned.await_exit()['state'], 'exited')
            identity.reset_mock()
            self.assertEqual(owned.await_exit()['state'], 'exited')
            identity.assert_not_called()
        self.assertEqual(calls, [])
        self.assertEqual(owned.queue.reads, 2)

    def test_incomplete_wait_retains_handle_for_later_exit_without_signals(self):
        owned, calls = registered([[], [], [], ['exit']], 0)
        with patch.object(kernel, 'identity', return_value=owned.before):
            with self.assertRaisesRegex(RuntimeError, 'exit observation incomplete'):
                owned.await_exit()
            self.assertFalse(owned.exited)
            self.assertEqual(owned.task.value, 7)
            self.assertEqual(owned.await_exit()['state'], 'exited')
        self.assertEqual(calls, [])
        self.assertEqual(owned.queue.reads, 4)

    def test_replaced_identity_refuses_before_wait(self):
        owned, calls = registered([[]], 0)
        with patch.object(kernel, 'identity', return_value={**owned.before, 'generation': 'foreign'}):
            with self.assertRaisesRegex(RuntimeError, 'identity changed'):
                owned.await_exit()
        self.assertEqual(calls, [])
        self.assertEqual(owned.queue.reads, 1)


if __name__ == '__main__':
    unittest.main()
