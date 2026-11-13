use hand_android_core::{NAME_MAX, PairError, parse_answer, parse_link, parse_open, valid_name};
use serde_json::Value;

fn vectors() -> Value {
    serde_json::from_str(include_str!("../../../docs/companion-vectors.json")).unwrap()
}

#[test]
fn every_valid_vector_yields_its_fields_and_open_path() {
    let data = vectors();
    assert_eq!(data["contract"], 1);
    let valid = data["valid"].as_array().unwrap();
    assert_eq!(valid.len(), 13);
    for vector in valid {
        let text = vector["link"].as_str().unwrap();
        let name = vector["name"].as_str().unwrap();
        let link = parse_link(text).unwrap_or_else(|reject| {
            panic!("{name}: rejected as {}", reject.reason());
        });
        let parsed = &vector["parsed"];
        assert_eq!(parsed["version"].as_u64(), Some(1), "{name}");
        assert_eq!(
            parsed["endpoint_id"].as_str(),
            Some(link.endpoint_id.as_str()),
            "{name}"
        );
        assert_eq!(
            parsed["fleet"].as_str(),
            Some(link.fleet.as_str()),
            "{name}"
        );
        assert_eq!(parsed["code"].as_str(), Some(link.code.as_str()), "{name}");
        assert_eq!(
            parsed["open_path"].as_str(),
            Some(link.open_path().as_str()),
            "{name}"
        );
        match &parsed["relay"] {
            Value::Null => assert!(link.relay.is_none(), "{name}"),
            Value::String(relay) => {
                assert_eq!(Some(relay.as_str()), link.relay.as_deref(), "{name}")
            }
            other => panic!("{name}: relay is {other}"),
        }
    }
}

#[test]
fn every_invalid_vector_yields_its_reason() {
    let data = vectors();
    let invalid = data["invalid"].as_array().unwrap();
    assert_eq!(invalid.len(), 73);
    for vector in invalid {
        let text = vector["link"].as_str().unwrap();
        let name = vector["name"].as_str().unwrap();
        let expected = vector["reject"].as_str().unwrap();
        let reject = parse_link(text).expect_err(name);
        assert_eq!(reject.reason(), expected, "{name}");
    }
}

#[test]
fn every_open_vector_parses_or_is_rejected() {
    let open = &vectors()["open"];
    let valid = open["valid"].as_array().unwrap();
    assert_eq!(valid.len(), 6);
    for vector in valid {
        let text = vector["link"].as_str().unwrap();
        let link = parse_open(text).expect(text);
        let parsed = &vector["parsed"];
        assert_eq!(
            parsed["fleet"].as_str(),
            Some(link.fleet.as_str()),
            "{text}"
        );
        assert_eq!(parsed["page"].as_str(), Some(link.page.as_str()), "{text}");
        assert_eq!(parsed["report"].as_str(), link.report.as_deref(), "{text}");
        assert_eq!(
            parsed["path"].as_str(),
            Some(link.path().as_str()),
            "{text}"
        );
    }
    let invalid = open["invalid"].as_array().unwrap();
    assert_eq!(invalid.len(), 61);
    for vector in invalid {
        let text = vector["link"].as_str().unwrap();
        assert_eq!(parse_open(text), None, "{text:?}");
    }
}

#[test]
fn the_pair_vectors_fix_the_name_the_body_and_each_answer() {
    let data = vectors();
    let pair = &data["pair"];
    assert_eq!(pair["name_max_runes"].as_u64(), Some(NAME_MAX as u64));
    assert_eq!(pair["request_fields"], serde_json::json!(["code", "name"]));
    assert_eq!(pair["body_max_bytes"], 4096);
    let exchanges = pair["exchanges"].as_array().unwrap();
    assert_eq!(exchanges.len(), 7);
    for vector in exchanges {
        let name = vector["name"].as_str().unwrap();
        let request_name = vector["request"]["name"].as_str().unwrap();
        let status = vector["status"].as_u64().unwrap() as u16;
        match vector["error"].as_str() {
            Some("bad_name") => assert!(valid_name(request_name).is_none(), "{name}"),
            Some(code) => {
                assert!(valid_name(request_name).is_some(), "{name}");
                let body = format!(r#"{{"error":{{"code":"{code}","message":"x"}}}}"#);
                let error = parse_answer(status, body.as_bytes()).err();
                assert_eq!(error.map(PairError::name), Some(code), "{name}");
            }
            None => {
                assert_eq!(valid_name(request_name), Some(request_name), "{name}");
                let value = format!("7.{}", "ab".repeat(32));
                let fleets: Vec<_> = vector["fleets"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|fleet| {
                        let id = "f0123456789ab";
                        assert_eq!(fleet["cookie_name"], "hand_phone_<fleet id>", "{name}");
                        serde_json::json!({"id": id, "name": fleet["name"], "cookie_name": format!("hand_phone_{id}"), "cookie_value": value})
                    })
                    .collect();
                let body = serde_json::json!({"version": 1, "fleet": {"id": "f0123456789ab", "name": "hand"}, "data": {"fleets": fleets, "missed": 0}});
                let paired = parse_answer(status, body.to_string().as_bytes())
                    .ok()
                    .unwrap();
                assert_eq!(paired.len(), fleets.len(), "{name}");
            }
        }
    }
    let regex = pair["cookie_value"].as_str().unwrap();
    assert_eq!(regex, "^[1-9][0-9]*\\.[0-9a-f]{64}$");
}
