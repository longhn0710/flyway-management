import contextlib
import importlib.util
import io
import os
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('prepare_auth', Path(__file__).with_name('prepare-auth.py'))
auth = importlib.util.module_from_spec(spec)
spec.loader.exec_module(auth)


class OTLPHeadersTest(unittest.TestCase):
    def test_full_sdk_header_and_percent_encoding(self):
        key = 'isolated-synthetic-fixture-key+' + 'a' * 24 + '=='
        expected = {'Authorization': 'ApiKey ' + key}
        self.assertEqual(auth.authorization_header('Authorization=ApiKey%20' + key), expected)
        self.assertEqual(auth.authorization_header(' authorization =apikey ' + key), expected)
        self.assertEqual(auth.authorization_header('Authorization=ApiKey%20' + key.replace('==', '%3D%3D')), expected)

    def test_rejects_raw_key_duplicates_extra_headers_and_injection(self):
        for value in ['', 'raw-key-only', 'Authorization=Bearer ' + 'a' * 64,
                      'Authorization=ApiKey ' + 'a' * 64 + ',Authorization=ApiKey ' + 'a' * 64,
                      'Authorization=ApiKey ' + 'a' * 64 + ',X-Other=unused',
                      'Authorization=ApiKey%0A' + 'a' * 64,
                      'Authorization=ApiKey ' + 'a' * 64 + '\r\n',
                      'Authorization=ApiKey%XX' + 'a' * 64,
                      'Authorization=ApiKey%20${env:PRIVATE}',
                      'Authorization=ApiKey%20' + 'a' * 17000]:
            with self.subTest(case=value[:20]), self.assertRaises(ValueError):
                auth.authorization_header(value)

    def test_output_is_private_and_no_secret_is_logged(self):
        secret = 'ApiKey ' + 'private-synthetic-fixture-' * 3
        buffer = io.StringIO()
        output = io.StringIO()
        # Assert the fixed output file is private and created without overwrite.
        with patch.dict(os.environ, {'OTEL_EXPORTER_OTLP_HEADERS': 'Authorization=' + secret}), \
             patch.object(auth.os, 'open', return_value=9) as opened, \
             patch.object(auth.os, 'fdopen', return_value=contextlib.nullcontext(output)), \
             contextlib.redirect_stdout(buffer):
            auth.main()
        self.assertEqual(opened.call_args.args[1:], (os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600))
        self.assertEqual(buffer.getvalue(), 'OTLP_AUTH_HEADER_FORMAT_VALID\n')
        self.assertNotIn(secret, buffer.getvalue())


if __name__ == '__main__':
    unittest.main()
