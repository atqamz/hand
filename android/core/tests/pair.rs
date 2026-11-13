#![cfg(target_os = "linux")]

mod support;

use hand_android_core::{Link, PairError, RequestError, redeem};
use serde_json::{Value, json};
use support::{Board, FeedBoard, Reply, Sidecar, SidecarOptions, dialer, sidecar_bin};

const FLEET: &str = "f0123456789ab";
const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const VALUE: &str = "7.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

fn link() -> Link {
    Link {
        version: 1,
        endpoint_id: "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6".to_owned(),
        fleet: FLEET.to_owned(),
        code: "K7QM3XRD2P".to_owned(),
        relay: None,
    }
}

fn vectors() -> Vec<Value> {
    let data: Value =
        serde_json::from_str(include_str!("../../../docs/companion-vectors.json")).unwrap();
    data["pair"]["exchanges"].as_array().unwrap().clone()
}

fn reply(vector: &Value) -> Reply {
    let status = vector["status"].as_u64().unwrap() as u16;
    let body = if status == 200 {
        let fleets: Vec<Value> = vector["fleets"]
            .as_array()
            .unwrap()
            .iter()
            .map(|fleet| {
                json!({
                    "id": FLEET,
                    "name": fleet["name"],
                    "cookie_name": format!("hand_phone_{FLEET}"),
                    "cookie_value": VALUE,
                })
            })
            .collect();
        json!({"version": 1, "fleet": {"id": FLEET, "name": "hand"}, "data": {"fleets": fleets, "missed": 0}})
    } else {
        json!({"error": {"code": vector["error"], "message": "the Board's sentence"}})
    };
    let mut reply = Reply::json(body.to_string());
    reply.status = status;
    reply
}

#[tokio::test]
async fn every_pair_vector_is_sent_as_the_contract_says_and_answered_as_it_says() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    for vector in vectors() {
        let label = vector["name"].as_str().unwrap().to_owned();
        let answer = reply(&vector);
        let board = FeedBoard::start(SECRET, move |_, _| Reply {
            status: answer.status,
            body: answer.body.clone(),
            hold: answer.hold,
            length: answer.length,
            headers: answer.headers.clone(),
            raw: None,
        })
        .await;
        let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).secret(SECRET));
        let dialer = dialer(sidecar.target()).await;
        let name = vector["request"]["name"].as_str().unwrap();

        let result = redeem(&dialer, &link(), &format!("  {name}  ")).await;

        let seen = board.seen();
        if vector["error"] == "bad_name" {
            assert!(seen.is_empty(), "{label}: a bad name is not sent");
            assert_eq!(
                result.err(),
                Some(PairError::Refused("bad_name")),
                "{label}"
            );
            continue;
        }
        assert_eq!(seen.len(), 1, "{label}");
        assert_eq!(seen[0].method, "POST", "{label}");
        assert_eq!(seen[0].target, format!("/{FLEET}/api/v1/pair"), "{label}");
        assert_eq!(seen[0].cookie, None, "{label}");
        assert_eq!(seen[0].csrf, None, "{label}");
        assert_eq!(seen[0].host.as_deref(), Some("localhost"), "{label}");
        assert!(seen[0].body.len() <= 4096, "{label}");
        let sent: Value = serde_json::from_slice(&seen[0].body).unwrap();
        assert_eq!(sent, json!({"code": "K7QM3XRD2P", "name": name}), "{label}");
        match vector["error"].as_str() {
            None => {
                let paired = result.ok().unwrap_or_else(|| panic!("{label}"));
                assert_eq!(paired.len(), 1, "{label}");
                assert_eq!(paired[0].id, FLEET, "{label}");
                assert_eq!(paired[0].name, vector["fleets"][0]["name"], "{label}");
                assert_eq!(paired[0].value, VALUE, "{label}");
            }
            Some(code) => assert_eq!(result.err().map(PairError::name), Some(code), "{label}"),
        }
    }
}

#[tokio::test]
async fn a_laptop_that_resets_the_stream_fails_the_pairing_with_a_request_error() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let board = Board::start(SECRET, support::Behavior::Reset).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).secret(SECRET));
    let dialer = dialer(sidecar.target()).await;

    let result = redeem(&dialer, &link(), "Pixel").await;

    assert!(matches!(
        result.err(),
        Some(PairError::Request(
            RequestError::Lost | RequestError::BoardNotRunning | RequestError::TimedOut
        ))
    ));
}
