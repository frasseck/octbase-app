"""i18n / multi-language support tests.

Covers the i18n core module (js/i18n.js) directly via page.evaluate (load,
fallback, interpolation, pluralization) and the end-to-end language selector
(switching locale updates the UI, <html lang>, and persists across reloads).
"""

from conftest import SHORT, TIMEOUT, settle


class TestI18nCore:
    def test_default_locale_is_loaded(self, app):
        lang = app.evaluate("() => window.getLocale()")
        assert lang in ("en", "de")
        assert app.evaluate("() => document.documentElement.lang") == lang

    def test_t_translates_known_key(self, app):
        result = app.evaluate("() => window.t('nav.myWork')")
        assert result and result != "nav.myWork"

    def test_t_falls_back_to_key_for_missing_translation(self, app):
        result = app.evaluate("() => window.t('does.not.exist')")
        assert result == "does.not.exist"

    def test_t_interpolates_variables(self, app):
        result = app.evaluate(
            "() => window.t('task.prNumber', {number: 42})"
        )
        assert "42" in result

    def test_t_pluralization_singular_and_plural(self, app):
        one = app.evaluate("() => window.t('task.taskCount', {count: 1})")
        other = app.evaluate("() => window.t('task.taskCount', {count: 5})")
        assert "1" in one
        assert "5" in other
        assert one != other


class TestLanguageSelector:
    def test_selector_present_and_labelled(self, app):
        select = app.query_selector("#lang-select")
        assert select is not None
        assert select.get_attribute("aria-label")

    def test_switching_language_updates_ui_and_html_lang(self, app):
        app.select_option("#lang-select", "de")
        settle(app)
        assert app.evaluate("() => document.documentElement.lang") == "de"
        assert app.evaluate("() => window.getLocale()") == "de"
        # "My Work" -> "Meine Aufgaben" in the sidebar nav.
        assert app.locator(".sidebar-item", has_text="Meine Aufgaben").count() > 0

        # Switch back to English to leave shared state clean for other tests.
        app.select_option("#lang-select", "en")
        settle(app)
    def test_french_is_not_offered(self, app):
        # French was removed; only English and German are offered.
        values = app.eval_on_selector_all(
            "#lang-select option", "opts => opts.map(o => o.value)"
        )
        assert "fr" not in values
        assert set(values) == {"en", "de"}

    def test_locale_persists_after_reload(self, app):
        app.select_option("#lang-select", "de")
        settle(app)
        assert app.evaluate("() => localStorage.getItem('octbase.lang')") == "de"

        app.reload()
        app.wait_for_selector("#lang-select", timeout=TIMEOUT)
        assert app.evaluate("() => window.getLocale()") == "de"
        assert app.evaluate("() => document.documentElement.lang") == "de"

        # Reset back to English for other tests.
        app.select_option("#lang-select", "en")
        settle(app)
        app.evaluate("() => localStorage.setItem('octbase.lang', 'en')")


class TestLocalizedErrors:
    def test_validation_error_toast_is_translated(self, demo_board):
        demo_board.click(".board-toolbar [data-act='showCreateTask']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=TIMEOUT)
        demo_board.fill("#task-title", "")
        demo_board.click("#modal-submit")
        demo_board.wait_for_selector("#task-title-error", timeout=TIMEOUT)
        err = demo_board.query_selector("#task-title-error")
        expected = demo_board.evaluate("() => window.t('validation.titleRequired')")
        assert err.inner_text().strip() == expected
        demo_board.keyboard.press("Escape")
