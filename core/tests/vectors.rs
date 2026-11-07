use hand_android_core::parse_link;
use serde_json::Value;

fn vectors() -> Value {
    serde_json::from_str(include_str!("companion-vectors.json")).unwrap()
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
