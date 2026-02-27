"""Tests for sb.naming module."""

from sb.naming import generate_name, sanitize_dirname


class TestSanitizeDirname:
    def test_lowercase(self):
        assert sanitize_dirname("MyApp") == "myapp"

    def test_spaces_to_hyphens(self):
        assert sanitize_dirname("my app") == "my-app"

    def test_special_chars_removed(self):
        assert sanitize_dirname("my@app!v2") == "myappv2"

    def test_keeps_hyphens_and_underscores(self):
        assert sanitize_dirname("my-app_v2") == "my-app_v2"

    def test_collapses_multiple_hyphens(self):
        assert sanitize_dirname("my---app") == "my-app"

    def test_strips_leading_trailing_hyphens(self):
        assert sanitize_dirname("-my-app-") == "my-app"

    def test_empty_returns_sandbox(self):
        assert sanitize_dirname("") == "sandbox"

    def test_all_special_chars_returns_sandbox(self):
        assert sanitize_dirname("@#$%") == "sandbox"

    def test_mixed_spaces_and_special(self):
        # "My App @ v2.0" -> "my-app-@-v2.0" -> "my-app--v20" -> "my-app-v20"
        assert sanitize_dirname("My App @ v2.0") == "my-app-v20"

    def test_numeric_only(self):
        assert sanitize_dirname("123") == "123"

    def test_already_clean(self):
        assert sanitize_dirname("myapp") == "myapp"


class TestGenerateName:
    def test_format(self):
        name = generate_name("/home/user/projects/my-app")
        assert name.startswith("sb-my-app-")
        # Hash should be 8 hex characters
        hash_part = name.split("-")[-1]
        assert len(hash_part) == 8
        assert all(c in "0123456789abcdef" for c in hash_part)

    def test_deterministic(self):
        name1 = generate_name("/home/user/projects/my-app")
        name2 = generate_name("/home/user/projects/my-app")
        assert name1 == name2

    def test_different_paths_different_hashes(self):
        name1 = generate_name("/home/user/projects/my-app")
        name2 = generate_name("/home/user/projects/other-app")
        assert name1 != name2

    def test_same_dirname_different_paths(self):
        """Same directory name under different parents should produce different names."""
        name1 = generate_name("/home/user/projects/app")
        name2 = generate_name("/home/user/work/app")
        # Same dirname portion but different hash
        assert name1.startswith("sb-app-")
        assert name2.startswith("sb-app-")
        assert name1 != name2

    def test_uses_basename(self):
        name = generate_name("/deeply/nested/path/to/cool-project")
        assert name.startswith("sb-cool-project-")
