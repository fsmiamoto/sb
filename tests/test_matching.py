"""Tests for sb.matching module."""

from sb.matching import _extract_dirname, _score_match, find_matching_sandboxes
from sb.sandbox import SandboxInfo


class TestExtractDirname:
    def test_standard_name(self):
        assert _extract_dirname("sb-my-app-a1b2c3d4") == "my-app"

    def test_single_word_dirname(self):
        assert _extract_dirname("sb-project-abcd1234") == "project"

    def test_multi_hyphen_dirname(self):
        assert _extract_dirname("sb-my-cool-app-a1b2c3d4") == "my-cool-app"

    def test_no_match_returns_full_name(self):
        assert _extract_dirname("not-a-sandbox-name") == "not-a-sandbox-name"

    def test_wrong_prefix(self):
        assert _extract_dirname("xx-app-a1b2c3d4") == "xx-app-a1b2c3d4"

    def test_short_hash(self):
        """Hash must be exactly 8 hex chars."""
        assert _extract_dirname("sb-app-abc") == "sb-app-abc"

    def test_non_hex_hash(self):
        assert _extract_dirname("sb-app-zzzzzzzz") == "sb-app-zzzzzzzz"


def _make_sandbox(name: str) -> SandboxInfo:
    return SandboxInfo(name=name, workspace="/tmp", created_at="", container_id=None)


class TestScoreMatch:
    def test_exact_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("sb-my-app-a1b2c3d4", sb) == 0

    def test_exact_match_case_insensitive(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("SB-MY-APP-A1B2C3D4", sb) == 0

    def test_prefix_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("sb-my", sb) == 10

    def test_dirname_exact_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("my-app", sb) == 20

    def test_dirname_prefix_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("my-a", sb) == 30

    def test_dirname_contains(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("app", sb) == 40

    def test_substring_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("a1b2", sb) == 50

    def test_no_match(self):
        sb = _make_sandbox("sb-my-app-a1b2c3d4")
        assert _score_match("zzz", sb) is None


class TestFindMatchingSandboxes:
    def test_exact_match_returns_single(self, sample_sandboxes):
        results = find_matching_sandboxes("sb-my-app-a1b2c3d4", sample_sandboxes)
        assert len(results) == 1
        assert results[0].name == "sb-my-app-a1b2c3d4"

    def test_dirname_match(self, sample_sandboxes):
        results = find_matching_sandboxes("my-app", sample_sandboxes)
        assert len(results) == 1
        assert results[0].name == "sb-my-app-a1b2c3d4"

    def test_partial_match_sorted_by_quality(self, sample_sandboxes):
        results = find_matching_sandboxes("app", sample_sandboxes)
        # "my-app" has dirname-contains for "app", "api-server" does not contain "app"
        assert len(results) == 1
        assert results[0].name == "sb-my-app-a1b2c3d4"

    def test_prefix_match_multiple(self, sample_sandboxes):
        results = find_matching_sandboxes("sb-", sample_sandboxes)
        # All sandboxes start with "sb-"
        assert len(results) == 3

    def test_no_match_empty(self, sample_sandboxes):
        results = find_matching_sandboxes("zzz-nonexistent", sample_sandboxes)
        assert results == []

    def test_empty_list(self):
        results = find_matching_sandboxes("anything", [])
        assert results == []

    def test_results_sorted_by_score(self, sample_sandboxes):
        """Verify prefix matches come before substring matches."""
        results = find_matching_sandboxes("web", sample_sandboxes)
        # "web-frontend" has dirname-prefix for "web"
        assert results[0].name == "sb-web-frontend-e5f6a7b8"
