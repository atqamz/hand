#ifndef Version
#error Pass /DVersion=X.Y.Z
#endif
#ifndef Source
#error Pass /DSource=FOLDER, the one that holds hand.exe and hand-desktop.exe (and hand-iroh.exe with /DIroh)
#endif

[Setup]
AppId={{3BB75498-076C-47AD-916A-C35EDB756592}
AppName=Hand
AppVersion={#Version}
AppPublisher=atqamz
AppPublisherURL=https://github.com/atqamz/hand
VersionInfoVersion={#Version}
DefaultDirName={localappdata}\hand
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputBaseFilename=Hand-Setup-{#Version}-x64
SetupIconFile={#SourcePath}hand.ico
UninstallDisplayIcon={app}\bin\hand.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ChangesEnvironment=yes
CloseApplications=no
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
ConfirmUninstall=Remove %1 from this computer?%n%nYour fleets, the .secondhand folder in your user profile and the window's composer drafts are kept.
UninstalledAll=%1 was removed. Your fleets, the .secondhand folder in your user profile and the window's composer drafts were kept; delete them yourself if you no longer want them.

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop icon"; GroupDescription: "Additional icons:"; Flags: unchecked

[Files]
Source: "{#Source}\hand.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
#ifdef Iroh
Source: "{#Source}\hand-iroh.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
#endif
Source: "{#Source}\hand-desktop.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
Source: "{#SourcePath}hand.ico"; DestDir: "{app}\bin"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\Hand"; Filename: "{app}\bin\hand-desktop.exe"; WorkingDir: "{app}\bin"; IconFilename: "{app}\bin\hand.ico"; Comment: "Hand, the supervisor window for your agent fleets"; Flags: uninsneveruninstall; Check: StartMenuIsFree
Name: "{userdesktop}\Hand"; Filename: "{app}\bin\hand-desktop.exe"; WorkingDir: "{app}\bin"; IconFilename: "{app}\bin\hand.ico"; Comment: "Hand, the supervisor window for your agent fleets"; Flags: uninsneveruninstall; Tasks: desktopicon

[Run]
Filename: "{app}\bin\hand-desktop.exe"; WorkingDir: "{app}\bin"; Description: "Start Hand"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: files; Name: "{app}\bin\hand-iroh.exe"
Type: files; Name: "{app}\bin\*.old*"

[Code]
const
  Description = 'Hand, the supervisor window for your agent fleets';

function CreateFileW(Name: String; Access, Share, Security, Disposition, Flags, Template: Integer): Integer;
  external 'CreateFileW@kernel32.dll stdcall';
function CloseHandle(Handle: Integer): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';

function BinDir: String;
begin
  Result := RemoveBackslash(ExpandConstant('{app}\bin'));
end;

function InUse(const Path: String): Boolean;
var
  Handle: Integer;
begin
  Handle := CreateFileW(Path, $40000000, 7, 0, 3, 0, 0);
  Result := Handle = -1;
  if not Result then
    CloseHandle(Handle);
end;

function AnyInUse(const Pattern: String): Boolean;
var
  Find: TFindRec;
begin
  Result := False;
  if FindFirst(BinDir + '\' + Pattern, Find) then
  try
    repeat
      if (Find.Attributes and $10) = 0 then
        if InUse(BinDir + '\' + Find.Name) then
          Result := True;
    until not FindNext(Find);
  finally
    FindClose(Find);
  end;
end;

function StopMessage(const Again: String): String;
begin
  Result := '';
  if AnyInUse('hand.exe*') or AnyInUse('hand-iroh.exe*') then
    Result := 'A Board or a watcher of this Hand install is running, and this never ends a fleet''s work by itself. Stop them, then ' + Again + ' again:'
      + #13#10#13#10 + '  hand board stop' + #13#10 + '  hand watch stop';
end;

procedure CloseWindow;
var
  Exe: String;
  Code: Integer;
begin
  if not FileExists(BinDir + '\hand-desktop.exe') then
    Exit;
  Exe := BinDir + '\hand-desktop.exe';
  StringChangeEx(Exe, '''', '''''', True);
  Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    '-NoProfile -NonInteractive -Command "Get-Process -Name hand-desktop -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq ''' + Exe + ''' } | ForEach-Object { [void]$_.CloseMainWindow(); [void]$_.WaitForExit(15000) }"',
    '', SW_HIDE, ewWaitUntilTerminated, Code);
end;

function Blocked(const Again: String): String;
begin
  Result := StopMessage(Again);
  if Result <> '' then
    Exit;
  CloseWindow;
  if AnyInUse('hand-desktop.exe*') then
    Result := 'The Hand window is still open. Close it, then ' + Again + ' again.';
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := Blocked('run Setup');
  if Result <> '' then
    Log(Result);
end;

function InitializeUninstall: Boolean;
var
  Reason: String;
begin
  Reason := Blocked('uninstall');
  Result := Reason = '';
  if not Result then
  begin
    Log(Reason);
    SuppressibleMsgBox(Reason, mbError, MB_OK, IDOK);
  end;
end;

function IsHands(const Lnk: String): Boolean;
var
  Raw, Needle: AnsiString;
  I: Integer;
begin
  Needle := Chr(Length(Description)) + #0;
  for I := 1 to Length(Description) do
    Needle := Needle + Description[I] + #0;
  Result := LoadStringFromFile(Lnk, Raw) and (Pos(Needle, Raw) > 0);
end;

function StartMenuIsFree: Boolean;
var
  Lnk: String;
begin
  Lnk := ExpandConstant('{userprograms}\Hand.lnk');
  Result := not FileExists(Lnk) or IsHands(Lnk);
end;

procedure RemoveIfHands(const Lnk: String);
begin
  if FileExists(Lnk) and IsHands(Lnk) then
    DeleteFile(Lnk);
end;

procedure RemoveHandsLinks;
begin
  RemoveIfHands(ExpandConstant('{userprograms}\Hand.lnk'));
  RemoveIfHands(ExpandConstant('{userdesktop}\Hand.lnk'));
end;

procedure EditPath(Add: Boolean);
var
  Old, Rest, Item, Kept, Dir: String;
  Found: Boolean;
  P: Integer;
begin
  Dir := BinDir;
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', Old) then
    Old := '';
  Rest := Old;
  Kept := '';
  Found := False;
  while Rest <> '' do
  begin
    P := Pos(';', Rest);
    if P = 0 then
    begin
      Item := Rest;
      Rest := '';
    end
    else
    begin
      Item := Copy(Rest, 1, P - 1);
      Rest := Copy(Rest, P + 1, Length(Rest));
    end;
    if Item = '' then
      Continue;
    if CompareText(RemoveBackslash(Item), Dir) = 0 then
    begin
      Found := True;
      if not Add then
        Continue;
    end;
    if Kept <> '' then
      Kept := Kept + ';';
    Kept := Kept + Item;
  end;
  if Add and not Found then
  begin
    if Kept <> '' then
      Kept := Kept + ';';
    Kept := Kept + Dir;
  end;
  if Kept = Old then
    Exit;
  if Kept = '' then
    RegDeleteValue(HKCU, 'Environment', 'Path')
  else
    RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Kept);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    EditPath(True);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    RemoveHandsLinks;
    EditPath(False);
  end;
end;
