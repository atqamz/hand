use crate::companion::Companion;
use crate::http::{Method, Request, RequestError, Response};
use crate::sessions::{Jar, Sessions};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Fate {
    Done,
    Unreachable,
    TooOld,
    Unrecognised,
}

impl Fate {
    pub fn name(self) -> &'static str {
        match self {
            Self::Done => "done",
            Self::Unreachable => "unreachable",
            Self::TooOld => "too_old",
            Self::Unrecognised => "not_recognised",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Left {
    pub id: String,
    pub name: String,
    pub fate: Fate,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SignOut {
    Done,
    Left(Vec<Left>),
}

pub(crate) struct Verdict {
    pub fate: Fate,
    pub host_down: bool,
}

fn settled(fate: Fate) -> Verdict {
    Verdict {
        fate,
        host_down: false,
    }
}

fn judge(result: Result<Response, RequestError>) -> Result<Response, Verdict> {
    match result {
        Ok(response) => match response.status {
            200 => Ok(response),
            401 => Err(settled(Fate::Done)),
            403 => Err(settled(Fate::Unrecognised)),
            404 | 405 => Err(settled(Fate::TooOld)),
            _ => Err(settled(Fate::Unreachable)),
        },
        Err(RequestError::Unpaired) => Err(settled(Fate::Done)),
        Err(RequestError::Malformed) => Err(settled(Fate::Unreachable)),
        Err(_) => Err(Verdict {
            fate: Fate::Unreachable,
            host_down: true,
        }),
    }
}

async fn send<J: Jar>(
    companion: &Companion,
    sessions: &Sessions<J>,
    fleet: &str,
    method: Method,
    path: &str,
    csrf: Option<&str>,
) -> Result<Response, RequestError> {
    let cookie = sessions.cookie(fleet).ok_or(RequestError::Unpaired)?;
    let request = Request::new(method, fleet, path, Some(&cookie), csrf, b"")?;
    companion.request(sessions, fleet, &cookie, &request).await
}

fn csrf_of(session: &Response) -> Option<String> {
    let body: serde_json::Value = serde_json::from_slice(&session.body).ok()?;
    body["data"]["csrf"].as_str().map(str::to_owned)
}

pub(crate) async fn sign_out_fleet<J: Jar>(
    companion: &Companion,
    sessions: &Sessions<J>,
    fleet: &str,
) -> Verdict {
    let session = send(
        companion,
        sessions,
        fleet,
        Method::Get,
        "/api/v1/session",
        None,
    )
    .await;
    let session = match judge(session) {
        Ok(session) => session,
        Err(verdict) => return verdict,
    };
    let Some(csrf) = csrf_of(&session) else {
        return settled(Fate::Unreachable);
    };
    let revoked = send(
        companion,
        sessions,
        fleet,
        Method::Post,
        "/api/v1/session/revoke",
        Some(&csrf),
    )
    .await;
    match judge(revoked) {
        Ok(_) => settled(Fate::Done),
        Err(verdict) => verdict,
    }
}
