use std::ffi::{CString, c_char};
use std::io;
use std::mem;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::PathBuf;
use std::ptr;
use std::sync::{Arc, LazyLock, Mutex};

use hand_android_core::{
    Alert, AlertSink, Cancel, CipherError, Companion, CompanionConfig, Feed, Fleet, Jar, KeyCipher,
    Live, LiveSink, Method, OPEN_REJECTED, PairError, RESCAN, Request, RequestError, Response,
    Sessions, Supervisor, open_paired, paired_fleets,
};
use jni::objects::{GlobalRef, JByteArray, JClass, JObject, JString, JValue};
use jni::sys::{jboolean, jlong, jstring};
use jni::{JNIEnv, JavaVM};

const PROBE: i32 = 0;
const SEAL: i32 = 1;
const OPEN: i32 = 2;

type Alerts = Supervisor<Arc<Sessions<HostJar>>, HostSink>;

struct Core {
    companion: Arc<Companion>,
    alerts: Arc<Alerts>,
    sessions: Arc<Sessions<HostJar>>,
    host: Arc<Host>,
    live: Mutex<Option<RunningLive>>,
}

struct RunningLive {
    task: tokio::task::JoinHandle<()>,
    cancel: Cancel,
}

impl RunningLive {
    fn stop(self) {
        self.cancel.cancel();
        self.task.abort();
    }
}

impl Core {
    fn start_live(&self, fleet: &str) {
        let mut slot = self.live.lock().unwrap();
        if let Some(running) = slot.take() {
            running.stop();
        }
        let live = Live::new(self.companion.dialer());
        let sessions = self.sessions.clone();
        let sink = HostLive(self.host.clone());
        let cancel = Cancel::default();
        let token = cancel.clone();
        let id = fleet.to_owned();
        let task = runtime().spawn(async move {
            let _ = live.run(&id, &sessions, &sink, &token).await;
        });
        *slot = Some(RunningLive { task, cancel });
    }

    fn stop_live(&self) {
        if let Some(running) = self.live.lock().unwrap().take() {
            running.stop();
        }
    }
}

struct State {
    core: Option<Arc<Core>>,
}

static STATE: Mutex<State> = Mutex::new(State { core: None });
static RUNTIME: LazyLock<tokio::runtime::Runtime> = LazyLock::new(|| {
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .expect("the core runtime builds")
});

fn runtime() -> &'static tokio::runtime::Runtime {
    &RUNTIME
}

pub fn forget_alerts() -> io::Result<()> {
    let core = STATE.lock().unwrap().core.clone();
    let Some(core) = core else {
        return Ok(());
    };
    core.stop_live();
    core.alerts.forget_all();
    let cleared = core.sessions.forget();
    let _ = core.host.stop_service();
    if cleared.is_err() {
        core.alerts.stop();
        warn("the web view's cookies or the session store could not be cleared");
    }
    cleared
}

unsafe extern "C" {
    fn __android_log_write(priority: i32, tag: *const c_char, text: *const c_char) -> i32;
}

fn warn(message: &str) {
    const WARN: i32 = 5;
    if let Ok(text) = CString::new(message) {
        unsafe { __android_log_write(WARN, c"HandAlerts".as_ptr(), text.as_ptr()) };
    }
}

struct Host {
    vm: JavaVM,
    object: GlobalRef,
}

impl Host {
    fn call<R>(&self, call: impl FnOnce(&mut JNIEnv) -> jni::errors::Result<R>) -> io::Result<R> {
        let mut env = self.vm.attach_current_thread().map_err(io::Error::other)?;
        let result = call(&mut env);
        if env.exception_check().unwrap_or(true) {
            let _ = env.exception_clear();
            return Err(io::Error::other("the Android side threw"));
        }
        result.map_err(io::Error::other)
    }

    fn keystore(&self, operation: i32, data: &[u8]) -> io::Result<Vec<u8>> {
        self.call(|env| {
            let input = env.byte_array_from_slice(data)?;
            let output = env
                .call_method(
                    &self.object,
                    "keystore",
                    "(I[B)[B",
                    &[JValue::Int(operation), JValue::Object(&input)],
                )?
                .l()?;
            env.convert_byte_array(JByteArray::from(output))
        })
    }

    fn cookies(&self) -> io::Result<Option<String>> {
        self.call(|env| {
            let header = env
                .call_method(&self.object, "cookies", "()Ljava/lang/String;", &[])?
                .l()?;
            if header.as_raw().is_null() {
                return Ok(None);
            }
            Ok(Some(env.get_string(&JString::from(header))?.into()))
        })
    }

    fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<bool> {
        self.call(|env| {
            let id = env.new_string(&fleet.id)?;
            let name = env.new_string(&fleet.name)?;
            let kind = env.new_string(&alert.kind)?;
            let reference = env.new_string(&alert.reference)?;
            let text = env.new_string(&alert.text)?;
            let link = match &alert.link {
                Some(link) => JObject::from(env.new_string(link)?),
                None => JObject::null(),
            };
            env.call_method(
                &self.object,
                "alert",
                "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Z",
                &[
                    JValue::Object(&id),
                    JValue::Object(&name),
                    JValue::Object(&kind),
                    JValue::Object(&reference),
                    JValue::Object(&text),
                    JValue::Object(&link),
                ],
            )?
            .z()
        })
    }

    fn clear_cookies(&self) -> io::Result<bool> {
        self.call(|env| {
            env.call_method(&self.object, "clearCookies", "()Z", &[])?
                .z()
        })
    }

    fn unreadable(&self) -> io::Result<()> {
        self.call(|env| env.call_method(&self.object, "unreadable", "()V", &[])?.v())
    }

    fn stop_service(&self) -> io::Result<()> {
        self.call(|env| {
            env.call_method(&self.object, "stopService", "()V", &[])?
                .v()
        })
    }

    fn unpaired(&self, fleet: &str, name: Option<&str>) -> io::Result<()> {
        self.call(|env| {
            let id = env.new_string(fleet)?;
            let name = match name {
                Some(name) => JObject::from(env.new_string(name)?),
                None => JObject::null(),
            };
            env.call_method(
                &self.object,
                "unpaired",
                "(Ljava/lang/String;Ljava/lang/String;)V",
                &[JValue::Object(&id), JValue::Object(&name)],
            )?
            .v()
        })
    }

    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()> {
        self.call(|env| {
            let fleet = env.new_string(fleet)?;
            let topic = env.new_string(topic)?;
            let json = env.new_string(json)?;
            env.call_method(
                &self.object,
                "live",
                "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)V",
                &[
                    JValue::Object(&fleet),
                    JValue::Object(&topic),
                    JValue::Object(&json),
                ],
            )?
            .v()
        })
    }

    fn link(&self, fleet: &str, state: &str, since: u64) -> io::Result<()> {
        self.call(|env| {
            let fleet = env.new_string(fleet)?;
            let state = env.new_string(state)?;
            env.call_method(
                &self.object,
                "link",
                "(Ljava/lang/String;Ljava/lang/String;J)V",
                &[
                    JValue::Object(&fleet),
                    JValue::Object(&state),
                    JValue::Long(since as jlong),
                ],
            )?
            .v()
        })
    }
}

struct HostLive(Arc<Host>);

impl LiveSink for HostLive {
    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()> {
        self.0.live(fleet, topic, json)
    }

    fn link(&self, fleet: &str, state: &str, since: u64) -> io::Result<()> {
        self.0.link(fleet, state, since)
    }
}

struct HostCipher(Arc<Host>);

impl HostCipher {
    fn run(&self, operation: i32, data: &[u8]) -> Result<Vec<u8>, CipherError> {
        let reply = self.0.keystore(operation, data)?;
        match reply.split_first() {
            Some((0, payload)) => Ok(payload.to_vec()),
            Some((_, failure)) => {
                let failure = String::from_utf8_lossy(failure);
                let (code, message) = failure.split_once('\n').unwrap_or((&failure, ""));
                Err(CipherError::from_plugin(Some(code), Some(message)))
            }
            None => Err(CipherError::Io(io::Error::other(
                "the Android Keystore answered nothing",
            ))),
        }
    }
}

impl KeyCipher for HostCipher {
    fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.run(SEAL, plaintext)
    }

    fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.run(OPEN, sealed)
    }

    fn probe(&self) -> Result<(), CipherError> {
        self.run(PROBE, &[]).map(|_| ())
    }
}

struct HostJar(Arc<Host>);

impl Jar for HostJar {
    fn cookies(&self) -> io::Result<Option<String>> {
        self.0.cookies()
    }

    fn clear(&self) -> io::Result<()> {
        if self.0.clear_cookies()? {
            Ok(())
        } else {
            Err(io::Error::other("the web view's cookies were not cleared"))
        }
    }
}

struct HostSink(Arc<Host>);

impl AlertSink for HostSink {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        if self.0.alert(fleet, alert)? {
            Ok(())
        } else {
            Err(io::Error::other("the notification was not posted"))
        }
    }

    async fn unpaired(&self, fleet: &str, name: Option<&str>) {
        let _ = self.0.unpaired(fleet, name);
    }
}

fn ensure_context(env: &mut JNIEnv, context: &JObject) -> Result<(), String> {
    if catch_unwind(ndk_context::android_context).is_ok() {
        return Ok(());
    }
    let vm = env.get_java_vm().map_err(|error| error.to_string())?;
    let global = env
        .new_global_ref(context)
        .map_err(|error| error.to_string())?;
    unsafe {
        ndk_context::initialize_android_context(
            vm.get_java_vm_pointer().cast(),
            global.as_obj().as_raw().cast(),
        );
    }
    mem::forget(global);
    Ok(())
}

fn start(
    env: &mut JNIEnv,
    data_dir: &JString,
    host: &JObject,
    context: &JObject,
) -> Result<(), String> {
    let mut state = STATE.lock().unwrap();
    if state.core.is_some() {
        return Ok(());
    }
    let result: Result<Core, String> = (|| {
        let data_dir = PathBuf::from(String::from(
            env.get_string(data_dir)
                .map_err(|error| error.to_string())?,
        ));
        ensure_context(env, context)?;
        let host = Arc::new(Host {
            vm: env.get_java_vm().map_err(|error| error.to_string())?,
            object: env
                .new_global_ref(host)
                .map_err(|error| error.to_string())?,
        });
        let cipher = Arc::new(HostCipher(host.clone()));
        let sessions = Sessions::open(&data_dir, HostJar(host.clone()), cipher.clone())
            .map(Arc::new)
            .map_err(|error| format!("the phone's pairings could not be read: {error}"))?;
        if sessions.quarantined() {
            let _ = host.unreadable();
        }
        let companion = runtime()
            .block_on(Companion::start(CompanionConfig {
                data_dir: data_dir.clone(),
                cipher,
            }))
            .map(Arc::new)
            .map_err(|error| format!("the phone's key could not start the companion: {error}"))?;
        let feed = Feed::new(companion.dialer(), data_dir.join("alerts"));
        let alerts = Supervisor::new(feed, sessions.clone(), HostSink(host.clone()), RESCAN);
        let restored = companion.clone();
        runtime().spawn(async move {
            let _ = restored.open_board().await;
        });
        Ok(Core {
            companion,
            alerts,
            sessions,
            host,
            live: Mutex::new(None),
        })
    })();
    match result {
        Ok(core) => {
            state.core = Some(Arc::new(core));
            Ok(())
        }
        Err(message) => Err(message),
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_nativeStart<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    data_dir: JString<'local>,
    host: JObject<'local>,
    context: JObject<'local>,
) -> jstring {
    match start(&mut env, &data_dir, &host, &context) {
        Ok(()) => ptr::null_mut(),
        Err(message) => env
            .new_string(message)
            .map_or(ptr::null_mut(), |text| text.into_raw()),
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_paired<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let sessions = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.sessions.clone());
    jboolean::from(sessions.is_some_and(|sessions| sessions.header().is_some()))
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_fleets<'local>(
    env: JNIEnv<'local>,
    _class: JClass<'local>,
) -> jstring {
    let sessions = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.sessions.clone());
    let ids = sessions
        .and_then(|sessions| sessions.header())
        .map(|header| paired_fleets(&header).join(" "))
        .unwrap_or_default();
    env.new_string(ids)
        .map_or(ptr::null_mut(), |text| text.into_raw())
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_watch<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) {
    let alerts = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.alerts.clone());
    if let Some(alerts) = alerts {
        runtime().block_on(async move { alerts.start() });
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unwatch<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) {
    let alerts = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.alerts.clone());
    if let Some(alerts) = alerts {
        alerts.stop();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_live<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
) {
    let fleet = match text(&mut env, &fleet) {
        Ok(Some(fleet)) => fleet,
        _ => return,
    };
    if let Some(core) = core() {
        core.start_live(&fleet);
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unlive<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.stop_live();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_resume<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        runtime().block_on(core.companion.reconnect_on_resume());
    }
}

fn text(env: &mut JNIEnv, value: &JString) -> Result<Option<String>, RequestError> {
    if value.as_raw().is_null() {
        return Ok(None);
    }
    env.get_string(value)
        .map(|text| Some(text.into()))
        .map_err(|_| RequestError::Malformed)
}

fn send(
    env: &mut JNIEnv,
    fleet: &JString,
    post: bool,
    path: &JString,
    csrf: &JString,
    body: &JByteArray,
) -> Result<Response, RequestError> {
    let core = STATE
        .lock()
        .unwrap()
        .core
        .clone()
        .ok_or(RequestError::NoLaptop)?;
    let fleet = text(env, fleet)?.ok_or(RequestError::Malformed)?;
    let path = text(env, path)?.ok_or(RequestError::Malformed)?;
    let csrf = text(env, csrf)?;
    let body = if body.as_raw().is_null() {
        Vec::new()
    } else {
        env.convert_byte_array(body)
            .map_err(|_| RequestError::Malformed)?
    };
    let cookies = core.sessions.header().unwrap_or_default();
    let method = if post { Method::Post } else { Method::Get };
    let request = if path == "/api/v1/meta" {
        Request::host(method, &fleet, &path, &cookies)?
    } else {
        Request::new(
            method,
            &fleet,
            &path,
            Some(&cookies),
            csrf.as_deref(),
            &body,
        )?
    };
    runtime().block_on(core.companion.request(&request))
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_request<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
    post: jboolean,
    path: JString<'local>,
    csrf: JString<'local>,
    body: JByteArray<'local>,
) -> jstring {
    let sent = catch_unwind(AssertUnwindSafe(|| {
        send(&mut env, &fleet, post != 0, &path, &csrf, &body)
    }));
    let answer = match sent {
        Ok(Ok(response)) => serde_json::json!({
            "status": response.status,
            "body": String::from_utf8_lossy(&response.body),
        }),
        Ok(Err(error)) => serde_json::json!({ "failure": error.name() }),
        Err(_) => serde_json::json!({ "failure": RequestError::Malformed.name() }),
    };
    reply(&mut env, answer)
}

fn reply(env: &mut JNIEnv, answer: serde_json::Value) -> jstring {
    env.new_string(answer.to_string())
        .or_else(|_| env.new_string(r#"{"failure":"malformed"}"#))
        .map_or(ptr::null_mut(), |text| text.into_raw())
}

fn core() -> Option<Arc<Core>> {
    STATE.lock().unwrap().core.clone()
}

fn failed(error: PairError) -> serde_json::Value {
    serde_json::json!({ "failure": error.name(), "message": error.message() })
}

fn offered(text: &str) -> serde_json::Value {
    let Some(core) = core() else {
        return failed(PairError::NoPending);
    };
    match core.companion.offer_link(text) {
        Ok(offer) => serde_json::json!({ "laptop": offer.laptop, "replaces": offer.replaces }),
        Err(error) => serde_json::json!({ "failure": "link", "message": error.message() }),
    }
}

fn paired(name: &str) -> serde_json::Value {
    let Some(core) = core() else {
        return failed(PairError::NoPending);
    };
    let stop_feeds = || {
        core.alerts.forget_all();
        core.stop_live();
    };
    match runtime().block_on(core.companion.pair(&core.sessions, name, stop_feeds)) {
        Ok(fleets) => serde_json::json!({
            "fleets": fleets.iter().map(|fleet| serde_json::json!({ "id": fleet.id, "name": fleet.name })).collect::<Vec<_>>(),
        }),
        Err(error) => failed(error),
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_offer<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    link: JString<'local>,
) -> jstring {
    let answer = catch_unwind(AssertUnwindSafe(|| match text(&mut env, &link) {
        Ok(Some(link)) => offered(&link),
        _ => serde_json::json!({ "failure": "link", "message": hand_android_core::LINK_REJECTED }),
    }))
    .unwrap_or_else(|_| failed(PairError::NoPending));
    reply(&mut env, answer)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_pair<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    name: JString<'local>,
) -> jstring {
    let answer = catch_unwind(AssertUnwindSafe(|| match text(&mut env, &name) {
        Ok(Some(name)) => paired(&name),
        _ => failed(PairError::Refused("bad_name")),
    }))
    .unwrap_or_else(|_| failed(PairError::NotSaved));
    reply(&mut env, answer)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_cancel<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.companion.cancel_pairing();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unsaved<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) -> jboolean {
    jboolean::from(core().is_some_and(|core| core.sessions.unsaved()))
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_open<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    link: JString<'local>,
) -> jstring {
    let answer = catch_unwind(AssertUnwindSafe(|| {
        let paired = core()
            .and_then(|core| core.sessions.header())
            .map(|header| paired_fleets(&header))
            .unwrap_or_default();
        let opened = match text(&mut env, &link) {
            Ok(Some(link)) => open_paired(&paired, &link),
            _ => Err(OPEN_REJECTED),
        };
        match opened {
            Ok(open) => serde_json::json!({ "path": open.path() }),
            Err(message) => serde_json::json!({ "message": message }),
        }
    }))
    .unwrap_or_else(|_| serde_json::json!({ "message": OPEN_REJECTED }));
    reply(&mut env, answer)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_fallback<'local>(
    mut env: JNIEnv<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
) -> jstring {
    let answer = catch_unwind(AssertUnwindSafe(|| {
        let board = text(&mut env, &fleet).ok().flatten().and_then(|fleet| {
            let core = core()?;
            let cookie = core.sessions.cookie(&fleet)?;
            Some((core.companion.port(), cookie))
        });
        match board {
            Some((port, cookie)) => serde_json::json!({ "port": port, "cookie": cookie }),
            None => serde_json::json!({}),
        }
    }))
    .unwrap_or_else(|_| serde_json::json!({}));
    reply(&mut env, answer)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_forget<'local>(
    _env: JNIEnv<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let forgotten = catch_unwind(AssertUnwindSafe(|| {
        core().is_none_or(|core| {
            runtime()
                .block_on(core.companion.forget_laptop(forget_alerts))
                .is_ok()
        })
    }));
    jboolean::from(forgotten.unwrap_or(false))
}
