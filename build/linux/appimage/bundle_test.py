#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("bundle", Path(__file__).with_name("bundle.py"))
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class DependencyClosureTest(unittest.TestCase):
    def test_installed_virtual_provider_is_bundled(self):
        dependencies = {
            "player": " |Depends: unavailable-jack\n  Depends: <libjack-0.125>\n    unavailable-jack\n    libjack0\n  Depends: removed\n  Recommends: omitted\n    also-omitted",
            "libjack0": "  Depends: libc6",
            "libc6": "",
        }

        def output(*args):
            if args[0] == "dpkg-query":
                self.assertIn(args[-1], dependencies)
                return "install ok installed"
            return dependencies[args[-1]]

        def status(args, **kwargs):
            if args[-1] == "removed":
                return subprocess.CompletedProcess(args, 0, "unknown ok not-installed", "")
            installed = args[-1] in dependencies
            return subprocess.CompletedProcess(args, 0 if installed else 1,
                                               "install ok installed" if installed else "", "")

        with patch.object(bundle, "output", side_effect=output), patch.object(bundle.subprocess, "run", side_effect=status):
            self.assertEqual(bundle.packages(["player"]), ["libc6", "libjack0", "player"])


if __name__ == "__main__":
    unittest.main()
