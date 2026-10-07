"""AAA negative checks for the consumer's dependency proof and fail-closed CLI."""
import contextlib
import io
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch

import check_chat as chat


class ConsumerGateTests(unittest.TestCase):
    def graph(self):
        return [{"Path": "fixture", "Main": True},
                {"Path": chat.CORE, "Version": "v0.13.1", "Sum": "h1:core", "GoModSum": "h1:mod"},
                {"Path": chat.PEER, "Version": chat.PEER_VERSION, "Sum": "h1:peer", "GoModSum": "h1:mod"}]

    def test_wrong_version_is_rejected(self):
        # Arrange
        graph = self.graph()
        graph[1]["Version"] = "v0.12.0"
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "core version mismatch"):
            chat.validate_graph(graph, "v0.13.1")

    def test_public_replace_cannot_pass(self):
        # Arrange
        graph = self.graph()
        graph[1]["Replace"] = {"Dir": "/tmp/core"}
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "unexpected module replacement"):
            chat.validate_graph(graph, "v0.13.1")

    def test_transitive_replace_cannot_pass_source(self):
        # Arrange
        graph = self.graph()
        graph[1]["Replace"] = {"Dir": str(chat.ROOT)}
        graph.append({"Path": "example.com/transitive", "Replace": {"Dir": "/tmp/other"}})
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "unexpected module replacement"):
            chat.validate_graph(graph, None, source=True)

    def test_peer_upgrade_rejected(self):
        # Arrange
        graph = self.graph()
        graph[2]["Version"] = "v0.16.0"
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "unsupported peer"):
            chat.validate_graph(graph, "v0.13.1")

    def test_missing_checksums_rejected(self):
        # Arrange
        graph = self.graph()
        del graph[2]["Sum"]
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "missing verified checksums"):
            chat.validate_graph(graph, "v0.13.1")

    def test_peer_sha_mismatch_rejected(self):
        # Arrange
        download = {"Path": chat.PEER, "Version": chat.PEER_VERSION,
                    "Sum": "h1:peer", "GoModSum": "h1:mod", "Origin": {"Hash": "bad"}}
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "peer resolved SHA"):
            chat.validate_peer_download(download)

    def test_missing_peer_sha_rejected(self):
        # Arrange
        download = {"Path": chat.PEER, "Version": chat.PEER_VERSION, "Sum": "h1:peer", "GoModSum": "h1:mod"}
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "peer resolved SHA"):
            chat.validate_peer_download(download)

    def test_consumer_failure_returns_nonzero_and_records_failure(self):
        # Arrange
        with tempfile.TemporaryDirectory() as temporary:
            report = Path(temporary) / "evidence.json"
            with patch.object(chat, "run", side_effect=OSError("consumer unavailable")), contextlib.redirect_stderr(io.StringIO()):
                # Act
                status = chat.main(["--published", "v0.13.1", "--report", str(report)])
            # Assert
            self.assertEqual(status, 1)
            self.assertIn('"status": "FAIL"', report.read_text())
            self.assertIn("consumer unavailable", report.read_text())

    def test_subprocess_failure_report_preserves_specific_reason(self):
        # Arrange
        error = subprocess.CalledProcessError(1, ["go", "mod", "tidy"], output="", stderr="checksum mismatch for peer")
        with tempfile.TemporaryDirectory() as temporary:
            report = Path(temporary) / "evidence.json"
            with patch.object(chat, "run", side_effect=error), contextlib.redirect_stderr(io.StringIO()):
                # Act
                status = chat.main(["--published", "v0.13.1", "--report", str(report)])
            # Assert
            self.assertEqual(status, 1)
            self.assertIn("checksum mismatch for peer", report.read_text())

    def test_public_mode_overrides_host_private_module_configuration(self):
        # Arrange
        host = {"GOPRIVATE": "github.com/skosovsky/*", "GONOPROXY": "github.com/skosovsky/*", "GONOSUMDB": "github.com/skosovsky/*"}
        with patch.dict(chat.os.environ, host), patch.object(chat, "run", side_effect=OSError("stop after env inspection")) as execute, contextlib.redirect_stderr(io.StringIO()):
            # Act
            status = chat.main(["--published", "v0.13.1"])
        # Assert
        self.assertEqual(status, 1)
        env = execute.call_args.args[2]
        for key in host:
            self.assertEqual(env[key], "none")
        self.assertEqual(env["GOENV"], "off")
        self.assertEqual(env["GOTOOLCHAIN"], "go" + chat.REGISTRY["toolchain"]["go"])
        self.assertEqual(env["GOSUMDB"], "sum.golang.org")
        self.assertEqual(env["GOPROXY"], "https://proxy.golang.org")

    def test_latest_is_not_exact_release_evidence(self):
        # Arrange
        arguments = ["--published", "latest"]
        # Act / Assert
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
            chat.main(arguments)
        self.assertEqual(raised.exception.code, 2)

    def test_baseline_cannot_substitute_candidate(self):
        # Arrange
        arguments = ["--candidate", "v0.13.1", "--baseline"]
        # Act / Assert
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
            chat.main(arguments)
        self.assertEqual(raised.exception.code, 2)

    def test_hidden_consumer_skip_is_rejected(self):
        # Arrange
        output = '{"Action":"skip","Test":"TestRequired"}\n{"Action":"pass","Package":"consumer"}\n'
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "required consumer tests skipped"):
            chat.validate_test_events(output)

    def test_compile_only_result_is_rejected(self):
        # Arrange
        output = '{"Action":"pass","Package":"consumer"}\n'
        # Act / Assert
        with self.assertRaisesRegex(ValueError, "no semantic consumer tests"):
            chat.validate_test_events(output)

    def test_command_package_without_tests_does_not_hide_semantic_pass(self):
        # Arrange
        output = '{"Action":"pass","Test":"TestSemantic","Package":"consumer"}\n{"Action":"skip","Package":"consumer/cmd/recipe"}\n'
        # Act
        result = chat.validate_test_events(output)
        # Assert
        self.assertIsNone(result)

    def test_transient_public_proxy_failure_retries(self):
        # Arrange
        attempts = [subprocess.CompletedProcess([], 1, "", "404 Not Found"), subprocess.CompletedProcess([], 0, "downloaded", "")]
        with patch.object(chat.subprocess, "run", side_effect=attempts) as execute, patch.object(chat.time, "sleep") as sleep:
            # Act
            output = chat.run(["go", "mod", "download", "all"], Path("/tmp"), {}, capture=True, public_retry=True)
        # Assert
        self.assertEqual(output, "downloaded")
        self.assertEqual(execute.call_count, 2)
        sleep.assert_called_once_with(5)

    def test_public_proxy_retry_is_bounded(self):
        # Arrange
        failure = subprocess.CompletedProcess([], 1, "", "503 Service Unavailable")
        with patch.object(chat.subprocess, "run", return_value=failure) as execute, patch.object(chat.time, "sleep") as sleep:
            # Act / Assert
            with self.assertRaises(subprocess.CalledProcessError):
                chat.run(["go", "mod", "download", "all"], Path("/tmp"), {}, public_retry=True)
        self.assertEqual(execute.call_count, 8)
        self.assertEqual(sleep.call_count, 7)

    def test_checksum_and_invalid_revision_never_retry(self):
        # Arrange
        for message in ("checksum mismatch", "invalid version: unknown revision", "SECURITY ERROR: checksum mismatch after 503 Service Unavailable"):
            failure = subprocess.CompletedProcess([], 1, "", message)
            with self.subTest(message=message), patch.object(chat.subprocess, "run", return_value=failure) as execute, patch.object(chat.time, "sleep") as sleep:
                # Act / Assert
                with self.assertRaises(subprocess.CalledProcessError):
                    chat.run(["go", "mod", "download", "all"], Path("/tmp"), {}, public_retry=True)
                self.assertEqual(execute.call_count, 1)
                sleep.assert_not_called()

    def test_json_stream_keeps_all_modules(self):
        # Arrange
        stream = ' {"Path":"one"}\n {"Path":"two"}\n'
        # Act
        graph = chat.json_stream(stream)
        # Assert
        self.assertEqual([entry["Path"] for entry in graph], ["one", "two"])


if __name__ == "__main__":
    unittest.main()
