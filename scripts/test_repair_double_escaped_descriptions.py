#!/usr/bin/env python3
"""Unit tests for the pure functions in repair-double-escaped-descriptions.py.

Fully offline — the script's main() never runs (import is guarded), and only
strip_extra_layers / escape_once / expected_stored are exercised. Run with:

    python3 scripts/test_repair_double_escaped_descriptions.py
"""

import importlib.util
import pathlib
import unittest

_PATH = pathlib.Path(__file__).with_name("repair-double-escaped-descriptions.py")
_SPEC = importlib.util.spec_from_file_location("repair_descriptions", _PATH)
repair = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(repair)


class StripExtraLayers(unittest.TestCase):
    def test_clean_string_untouched(self):
        s = "AT&amp;T serves &lt;b&gt; and &#8594; plain text"
        self.assertEqual(repair.strip_extra_layers(s), (s, 0))

    def test_single_layer(self):
        self.assertEqual(repair.strip_extra_layers("a &amp;gt; b"),
                         ("a &gt; b", 1))

    def test_deep_layers_counted_per_reference(self):
        repaired, worst = repair.strip_extra_layers(
            "&amp;amp;amp;gt; and &amp;lt;")
        self.assertEqual(repaired, "&gt; and &lt;")
        self.assertEqual(worst, 3)

    def test_legitimate_entities_survive_next_to_damage(self):
        # The regression the targeted rewrite exists to prevent: a global
        # "&amp;"->"&" pass would also decode the legitimate AT&amp;T.
        repaired, worst = repair.strip_extra_layers(
            "AT&amp;T stays, &amp;amp;gt; fixed")
        self.assertEqual(repaired, "AT&amp;T stays, &gt; fixed")
        self.assertEqual(worst, 2)

    def test_over_escaped_literal_ampersand(self):
        self.assertEqual(repair.strip_extra_layers("&amp;amp;"),
                         ("&amp;", 1))

    def test_numeric_and_hex_references(self):
        self.assertEqual(repair.strip_extra_layers("&amp;#8594; &amp;#x27;"),
                         ("&#8594; &#x27;", 1))

    def test_repair_is_idempotent(self):
        once, _ = repair.strip_extra_layers("x &amp;amp;amp;lt; y")
        twice, worst = repair.strip_extra_layers(once)
        self.assertEqual(twice, once)
        self.assertEqual(worst, 0)


class EscapeOnce(unittest.TestCase):
    def test_raw_specials_escaped(self):
        self.assertEqual(repair.escape_once("a < b > c & d"),
                         "a &lt; b &gt; c &amp; d")

    def test_existing_entities_preserved(self):
        self.assertEqual(repair.escape_once("&lt;kept&gt; &#x27; &amp;"),
                         "&lt;kept&gt; &#x27; &amp;")

    def test_bare_ampersand_not_forming_entity(self):
        self.assertEqual(repair.escape_once("a & b"), "a &amp; b")

    def test_entity_shaped_run_preserved_like_go(self):
        # "&D;" matches the entity shape, so it is preserved — same as the Go
        # EscapeText this mirrors (htmlsafe.go entityRe has no name allowlist).
        # The mirror must reproduce that, not "improve" on it.
        self.assertEqual(repair.escape_once("R&D; & more"),
                         "R&D; &amp; more")

    def test_idempotent(self):
        s = "mix < of &amp; raw & and &lt;stored&gt;"
        self.assertEqual(repair.escape_once(repair.escape_once(s)),
                         repair.escape_once(s))


class ExpectedStored(unittest.TestCase):
    def test_tags_kept_text_escaped_and_trimmed(self):
        self.assertEqual(
            repair.expected_stored("  <b>a & b</b> -> done  "),
            "<b>a &amp; b</b> -&gt; done")

    def test_quoted_attr_with_gt_stays_one_tag(self):
        sent = '<a href="u?a=1&amp;b>2">x & y</a>'
        self.assertEqual(repair.expected_stored(sent),
                         '<a href="u?a=1&amp;b>2">x &amp; y</a>')

    def test_repaired_value_round_trips(self):
        # A repaired row (entities all singly-escaped, no raw specials in the
        # text runs) must equal its own expected_stored — that equality is the
        # read-back check the script performs after the PUT.
        repaired, _ = repair.strip_extra_layers(
            "<p>steps &amp;amp;gt; done, AT&amp;T</p>")
        self.assertEqual(repair.expected_stored(repaired), repaired)


if __name__ == "__main__":
    unittest.main()
