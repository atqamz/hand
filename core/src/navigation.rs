use url::Url;

pub fn may_load(url: &Url, bundled_origin: &Url, proxy_origin: Option<&Url>) -> bool {
    url.origin() == bundled_origin.origin()
        || proxy_origin.is_some_and(|proxy| url.origin() == proxy.origin())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::companion::{proxy_origin, proxy_url};

    #[test]
    fn only_the_bundled_page_and_the_proxy_load() {
        let bundled = Url::parse("http://tauri.localhost/").unwrap();
        let proxy = Url::parse("http://localhost:49152/").unwrap();
        let cases = [
            ("http://tauri.localhost/", true),
            ("http://tauri.localhost/index.html", true),
            ("http://localhost:49152/", true),
            ("http://localhost:49152/f0123456789ab/pair#v1.abc", true),
            ("http://localhost:49152/static/app.js", true),
            ("http://LOCALHOST:49152/", true),
            ("http://localhost:49152", true),
            ("http://127.0.0.1:49152/", false),
            ("http://localhost:49153/", false),
            ("https://localhost:49152/", false),
            ("http://localhost.:49152/", false),
            ("http://evil.localhost:49152/", false),
            ("http://evil.test/", false),
            ("http://localhost:49152@evil.test/", false),
            (
                "file:///data/user/0/io.github.atqamz.handcompanion/files/page.html",
                false,
            ),
            ("handboard://c#v1.abc", false),
        ];
        for (url, allowed) in cases {
            let url = Url::parse(url).unwrap();
            assert_eq!(may_load(&url, &bundled, Some(&proxy)), allowed, "{url}");
        }
    }

    #[test]
    fn an_unknown_proxy_origin_allows_only_the_bundled_page() {
        let bundled = Url::parse("http://tauri.localhost/").unwrap();
        for url in ["http://tauri.localhost/", "http://tauri.localhost/main.js"] {
            assert!(may_load(&Url::parse(url).unwrap(), &bundled, None), "{url}");
        }
        for url in [
            "http://localhost:0/",
            "http://localhost:49152/",
            "https://tauri.localhost/",
        ] {
            assert!(
                !may_load(&Url::parse(url).unwrap(), &bundled, None),
                "{url}"
            );
        }
    }

    #[test]
    fn a_clicked_link_stays_on_the_board_or_leaves_it() {
        let bundled = Url::parse("http://tauri.localhost/").unwrap();
        let proxy = Url::parse("http://localhost:49152/").unwrap();
        for url in [
            "http://localhost:49152/f0123456789ab/task/3",
            "http://localhost:49152/f0123456789ab/task/3#notes",
            "http://LOCALHOST:49152/f0123456789ab/task/3",
        ] {
            assert!(
                may_load(&Url::parse(url).unwrap(), &bundled, Some(&proxy)),
                "{url}"
            );
        }
        for url in [
            "https://github.com/atqamz/hand",
            "http://localhost:49153/f0123456789ab/task/3",
            "http://evil.test/f0123456789ab/task/3",
            "mailto:board@example.test",
            "tel:+15550100",
        ] {
            assert!(
                !may_load(&Url::parse(url).unwrap(), &bundled, Some(&proxy)),
                "{url}"
            );
        }
    }

    #[test]
    fn the_proxy_origin_is_the_url_the_app_loads() {
        let port = 49152;
        let bundled = Url::parse("http://tauri.localhost/").unwrap();
        let loaded = Url::parse(&proxy_url(port, "/static/app.js")).unwrap();
        assert_eq!(loaded.origin(), proxy_origin(port).origin());
        assert!(may_load(&loaded, &bundled, Some(&proxy_origin(port))));
    }
}
