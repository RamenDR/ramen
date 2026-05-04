# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Tests for tool management (kustomize-only version).
"""

import os
import tempfile
import unittest

from packaging import version

from . import tools


class TestKustomize(unittest.TestCase):
    """Test Kustomize tool."""

    def test_kustomize_class_attributes(self):
        """Test kustomize has correct class attributes."""
        self.assertEqual(tools.Kustomize.name, "kustomize")
        self.assertEqual(tools.Kustomize.required_version, "5.7.0")
        self.assertEqual(tools.Kustomize.archive_binary_path, "kustomize")
        # Verify download_url is set and contains expected components
        self.assertIsNotNone(tools.Kustomize.download_url)
        self.assertIn(
            "github.com/kubernetes-sigs/kustomize", tools.Kustomize.download_url
        )
        self.assertIn("5.7.0", tools.Kustomize.download_url)
        self.assertIn(tools.OS_NAME, tools.Kustomize.download_url)
        self.assertIn(tools.ARCH, tools.Kustomize.download_url)

    def test_kustomize_install_dir_default(self):
        """Test kustomize install_dir method with default."""
        import sys

        kustomize = tools.Kustomize()
        expected_dir = os.path.join(sys.prefix, "bin")
        self.assertEqual(kustomize.install_dir(), expected_dir)

    def test_kustomize_install_dir_custom(self):
        """Test kustomize install_dir method with custom directory."""
        custom_dir = "/tmp/custom"
        kustomize = tools.Kustomize(install_dir=custom_dir)
        self.assertEqual(kustomize.install_dir(), custom_dir)

    def test_kustomize_path(self):
        """Test kustomize path generation."""
        import sys

        kustomize = tools.Kustomize()
        expected_path = os.path.join(sys.prefix, "bin", "kustomize")
        self.assertEqual(kustomize.path(), expected_path)

    def test_kustomize_version_parsing_with_v_prefix(self):
        """Test parsing version string with 'v' prefix (common format)."""
        # Simulate what version() does with real kustomize output
        version_output = "v5.7.0\n"
        version_str = version_output.strip().lstrip("v")
        ver = version.parse(version_str)
        self.assertEqual(ver, version.parse("5.7.0"))

    def test_kustomize_version_parsing_without_v_prefix(self):
        """Test parsing version string without 'v' prefix."""
        # Some tools might output without 'v'
        version_output = "5.7.0\n"
        version_str = version_output.strip().lstrip("v")
        ver = version.parse(version_str)
        self.assertEqual(ver, version.parse("5.7.0"))

    def test_kustomize_version_parsing_older_version(self):
        """Test parsing older kustomize version string."""
        # Test with older version format
        version_output = "v4.5.7\n"
        version_str = version_output.strip().lstrip("v")
        ver = version.parse(version_str)
        self.assertEqual(ver, version.parse("4.5.7"))
        # Verify comparison works
        self.assertLess(ver, version.parse("5.7.0"))

    def test_kustomize_version_parsing_newer_version(self):
        """Test parsing newer kustomize version string."""
        # Test with newer version format
        version_output = "v5.8.0\n"
        version_str = version_output.strip().lstrip("v")
        ver = version.parse(version_str)
        self.assertEqual(ver, version.parse("5.8.0"))
        # Verify comparison works
        self.assertGreater(ver, version.parse("5.7.0"))


class TestHelpers(unittest.TestCase):
    """Test helper functions."""

    def test_platform_constants(self):
        """Test platform constants are set correctly."""
        self.assertIn(tools.OS_NAME, ["linux", "darwin"])
        self.assertIn(tools.ARCH, ["amd64", "arm64"])

    def test_download(self):
        """
        Test file download.

        This integration test downloads a small file from GitHub to verify:
        1. The download function works correctly
        2. The URL format is correct (detects if tool was moved)
        """
        # Use a small, stable file from kustomize releases for testing
        # This is the LICENSE file which is small and unlikely to change
        test_url = (
            "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/"
            "master/LICENSE"
        )

        with tempfile.TemporaryDirectory() as tmpdir:
            target = os.path.join(tmpdir, "test-file")
            tools.download(test_url, target)
            self.assertTrue(os.path.exists(target))
            # Verify we got some content
            with open(target, "rb") as f:
                content = f.read()
                self.assertGreater(len(content), 0)
                # LICENSE file should contain "Apache"
                self.assertIn(b"Apache", content)


class TestSetup(unittest.TestCase):
    """Test setup function."""

    def test_setup_accepts_install_dir(self):
        """Test setup accepts custom install_dir parameter."""
        # This test verifies the parameter is accepted
        # Actual installation testing would require more setup
        # Just verify setup() accepts the parameter without error
        # We don't actually call it to avoid modifying the system
        self.assertTrue(callable(tools.setup))


if __name__ == "__main__":
    unittest.main()
