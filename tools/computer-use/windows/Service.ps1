param([string]$Manifest = $env:AICLAW_COMPUTER_MANIFEST)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$script:servicePath = if ($PSCommandPath) { $PSCommandPath } else { $env:AICLAW_COMPUTER_SERVICE }
$script:serviceRoot = Split-Path -Parent $script:servicePath
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes, UIAutomationClientsideProviders, System.Drawing, System.Windows.Forms
$proxyAssembly=[AppDomain]::CurrentDomain.GetAssemblies() | Where-Object { $_.GetName().Name -eq 'UIAutomationClientsideProviders' } | Select-Object -First 1
Add-Type -TypeDefinition ([IO.File]::ReadAllText((Join-Path $script:serviceRoot 'Native.cs'))) -ReferencedAssemblies @('System.dll','System.Drawing.dll','System.Windows.Forms.dll',[System.Windows.Automation.AutomationElement].Assembly.Location)
[AIClawComputerUse.Native]::InitializeAutomation($proxyAssembly.GetName())
[void][AIClawComputerUse.Native]::SetThreadDpiAwarenessContext([IntPtr](-4))
$script:target = $null
$script:elements = @{}
$script:launches = @{}
$script:counter = 0
$script:imageSize = $null
$script:truncated = $false
$script:parentPid = [int]$env:AICLAW_COMPUTER_PARENT_PID
[byte[]]$source = [IO.File]::ReadAllBytes($script:servicePath) + [IO.File]::ReadAllBytes((Join-Path $script:serviceRoot 'Native.cs'))
$sha = [Security.Cryptography.SHA256]::Create()
$script:sourceHash = -join ($sha.ComputeHash($source) | ForEach-Object { $_.ToString('x2') }); $sha.Dispose()
$specs = Get-Content -Raw -LiteralPath $Manifest | ConvertFrom-Json
$supported = @('list_apps','find_apps','permission_status','frontmost_window','get_app_state','query','click','set_value','select_text','perform_secondary_action','press_key','type_text','scroll','drag','stop_app','modify_app','start_app')
$tools = @($specs | Where-Object { $supported -contains $_.action } | ForEach-Object {
    $_.schema.properties.PSObject.Properties.Remove('observation_id')
    $_.schema.properties.PSObject.Properties.Remove('verify_after')
    if ($null -ne $_.schema.PSObject.Properties['required']) { $_.schema.required = @($_.schema.required | Where-Object { $_ -ne 'observation_id' }) }
    else { Add-Member -InputObject $_.schema -MemberType NoteProperty -Name required -Value @() }
    @{name=$_.action;inputSchema=$_.schema}
})
function Value($object, [string]$name, $default=$null) {
    if ($null -eq $object) { return $default }
    $property = $object.PSObject.Properties[$name]
    if ($null -eq $property) { return $default }
    return $property.Value
}
function Text($value) {
    if ($value -isnot [string]) { $value = ConvertTo-Json -InputObject $value -Depth 20 -Compress }
    return @{content=@(@{type='text';text=$value})}
}
function Clear-Observation { $script:target=$null; $script:elements=@{}; $script:imageSize=$null }
function Check-Cancel { if ([AIClawComputerUse.Native]::ParentEnded) { throw 'Desktop request cancelled; no queued input will be replayed' } }
function Wait-Checked([double]$seconds) {
    $until=[DateTime]::UtcNow.AddSeconds($seconds)
    while ([DateTime]::UtcNow -lt $until) { Check-Cancel; [Threading.Thread]::Sleep(10) }
}
function Number($value, [double]$default, [double]$low, [double]$high) {
    if ($null -eq $value) { return $default }
    if ($value -is [bool] -or $value -is [string] -or [double]::IsNaN([double]$value) -or [double]::IsInfinity([double]$value) -or [double]$value -lt $low -or [double]$value -gt $high) { throw 'Invalid numeric desktop argument' }
    return [double]$value
}
function Frame($rect) { return @{x=$rect.Left;y=$rect.Top;width=$rect.Right-$rect.Left;height=$rect.Bottom-$rect.Top} }
function Installed-Apps([string]$needle) {
    $shell=New-Object -ComObject WScript.Shell
    try {
        foreach ($folder in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('CommonPrograms'))) {
            if (-not $folder) { continue }
            foreach ($file in Get-ChildItem -LiteralPath $folder -Filter '*.lnk' -Recurse -File -ErrorAction SilentlyContinue) {
                Check-Cancel
                if (($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -or ($needle -and $file.BaseName.IndexOf($needle,[StringComparison]::OrdinalIgnoreCase) -lt 0)) { continue }
                $shortcut=$null
                try {
                    $shortcut=$shell.CreateShortcut($file.FullName)
                    $executable=[IO.Path]::GetFileName($shortcut.TargetPath)
                    if (-not $executable -or $executable -match '^(aiclaw|electron|claw-agent|aiclawcomputeruse)(\.exe)?$') { continue }
                    $hash=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
                    $sha=[Security.Cryptography.SHA256]::Create()
                    try { $pathHash=-join ($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($file.FullName)) | ForEach-Object { $_.ToString('x2') }) } finally { $sha.Dispose() }
                    $name=[regex]::Replace($file.BaseName,'[^a-zA-Z0-9_.-]','_')
                    $id='launch:win32.'+$name+'.'+$pathHash.Substring(0,24)
                    $script:launches[$id]=@{path=$file.FullName;hash=$hash}
                    @{id=$id;launch_id=$id;display_name=$file.BaseName;is_running=$false;windows=@()}
                } finally { if ($null -ne $shortcut) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shortcut) } }
            }
        }
    } finally { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell) }
}
function Start-App($argsObject) {
    $id=Value $argsObject 'app'
    if ($id -isnot [string] -or -not $script:launches.ContainsKey($id)) { throw 'Use an exact launch_id discovered on this connection; arbitrary commands and paths are not accepted' }
    $entry=$script:launches[$id]
    if ((Get-FileHash -LiteralPath $entry.path -Algorithm SHA256).Hash -ne $entry.hash) { throw 'The discovered shortcut changed; discover it again before launch' }
    Clear-Observation; Check-Cancel
    $launched=[AIClawComputerUse.Native]::StartShortcut($entry.path)
    if ($null -ne $launched) {
        $deadline=[DateTime]::UtcNow.AddSeconds(3)
        while ([DateTime]::UtcNow -lt $deadline) {
            $windows=@([AIClawComputerUse.Native]::Windows($launched.PID) | Where-Object { $_.Birth -eq $launched.Birth })
            if ($windows.Count -gt 0) { return Text @{app=[string]$launched.PID;launch_id=$id;verified=$true;launch_requested=$true} }
            Wait-Checked 0.05
        }
    }
    return Text @{launch_id=$id;verified=$false;launch_requested=$true}
}
function Resolve-Target($argsObject) {
    $app=Value $argsObject 'app'
    if ($app -isnot [string] -or $app -notmatch '^[1-9][0-9]*$') { throw 'Use the exact running PID from application discovery' }
    $targetPid=[int]$app
    if ($targetPid -eq $PID -or $targetPid -eq $script:parentPid -or -not [AIClawComputerUse.Native]::SameUser($targetPid)) { throw 'Refusing helper, parent or another user process' }
    $windows=@([AIClawComputerUse.Native]::Windows($targetPid))
    $requested=Value $argsObject 'window_id'
    if ($null -ne $requested) { $windows=@($windows | Where-Object { $_.ID -eq [long]$requested }) }
    if ($windows.Count -ne 1) { throw 'Choose one explicit window_id from application discovery; target is missing or ambiguous' }
    $window=$windows[0]
    $root=[System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$window.ID)
    if ($root.Current.ProcessId -ne $targetPid) { throw 'UI Automation window does not belong to the selected process' }
    return @{app=$app;window=$window;root=$root}
}
function Pattern($node, $kind) {
    $pattern=$null
    if ($node.TryGetCurrentPattern($kind,[ref]$pattern)) { return $pattern }
    return $null
}
function Attributes($node) {
    $current=$node.Current
    $valuePattern=Pattern $node ([System.Windows.Automation.ValuePattern]::Pattern)
    $value=''; $settable=$false
    if ($valuePattern -and -not $current.IsPassword) { $value=$valuePattern.Current.Value; $settable=-not $valuePattern.Current.IsReadOnly }
    if ($value.Length -gt 1000) { $value=$value.Substring(0,1000);$script:truncated=$true }
    $name=$current.Name
    if ($name.Length -gt 1000) { $name=$name.Substring(0,1000);$script:truncated=$true }
    $actions=@()
    foreach ($entry in @(@('invoke',[System.Windows.Automation.InvokePattern]::Pattern),@('toggle',[System.Windows.Automation.TogglePattern]::Pattern),@('select',[System.Windows.Automation.SelectionItemPattern]::Pattern),@('expand',[System.Windows.Automation.ExpandCollapsePattern]::Pattern))) {
        if ($null -ne (Pattern $node $entry[1])) { $actions += $entry[0] }
    }
    return @{role=$current.ControlType.ProgrammaticName.Replace('ControlType.','');title=$name;identifier=$current.AutomationId;value=$value;settable=$settable;actions=$actions;
        frame=@{x=$current.BoundingRectangle.X;y=$current.BoundingRectangle.Y;width=$current.BoundingRectangle.Width;height=$current.BoundingRectangle.Height}}
}
function Fingerprint($node) {
    $current=$node.Current
    return ([ordered]@{runtime=($node.GetRuntimeId() -join ',');name=$current.Name;identifier=$current.AutomationId;role=$current.ControlType.Id;bounds=$current.BoundingRectangle.ToString()} | ConvertTo-Json -Compress)
}
function Nodes($root, [int]$maximum=1200) {
    $pending=New-Object 'Collections.Generic.Queue[object]'
    $pending.Enqueue(@{node=$root;depth=0})
    $walker=[System.Windows.Automation.TreeWalker]::RawViewWalker
    $deadline=[DateTime]::UtcNow.AddSeconds(2); $visited=0; $script:truncated=$false
    while ($pending.Count -gt 0 -and $visited -lt $maximum -and [DateTime]::UtcNow -lt $deadline) {
        Check-Cancel; $item=$pending.Dequeue(); $visited++
        Write-Output $item
        if ($item.depth -ge 64) { $script:truncated=$true; continue }
        $child=$walker.GetFirstChild($item.node)
        while ($null -ne $child -and $pending.Count+$visited -lt $maximum) {
            $pending.Enqueue(@{node=$child;depth=$item.depth+1}); $child=$walker.GetNextSibling($child)
        }
        if ($null -ne $child) { $script:truncated=$true }
    }
    if ($pending.Count -gt 0) { $script:truncated=$true }
}
function Retain($node) {
    $script:counter++
    $script:elements[[string]$script:counter]=@{node=$node;fingerprint=(Fingerprint $node)}
    return $script:counter
}
function Element($index, $targetObject) {
    if ($index -isnot [string] -or -not $script:elements.ContainsKey($index)) { throw 'Element was not observed on this connection or is stale' }
    $saved=$script:elements[$index]; $node=$saved.node
    if ($node.Current.ProcessId -ne $targetObject.window.PID -or (Fingerprint $node) -ne $saved.fingerprint) { throw 'Element identity or geometry changed; observe again' }
    $parent=$node; $rootId=$targetObject.root.GetRuntimeId() -join ','
    for ($i=0;$i -lt 64 -and $null -ne $parent;$i++) {
        if (($parent.GetRuntimeId() -join ',') -eq $rootId) { return $node }
        $parent=[System.Windows.Automation.TreeWalker]::RawViewWalker.GetParent($parent)
    }
    throw 'Element no longer belongs to the observed window'
}
function Observe($argsObject, [bool]$queryOnly) {
    Clear-Observation
    try {
        $targetObject=Resolve-Target $argsObject; $script:target=$targetObject
        $metadata=@{app=$targetObject.app;pid=$targetObject.window.PID;window_id=$targetObject.window.ID;process_start_time=$targetObject.window.Birth;window_frame=(Frame $targetObject.window.Frame);truncated=$false;image_width=0;image_height=0}
        $maximum=[int](Number (Value $argsObject $(if ($queryOnly) {'max_nodes'} else {'max_tree_nodes'})) 1200 1 5000)
        $limit=[int](Number (Value $argsObject 'limit') 20 1 100)
        $needle=Value $argsObject 'text' ''; $role=Value $argsObject 'role' ''; $exact=Value $argsObject 'exact' $false
        if ($queryOnly -and (($needle -isnot [string]) -or ($role -isnot [string]) -or ($needle.Length -gt 1000) -or ($role.Length -gt 1000) -or (-not $needle.Trim() -and -not $role.Trim()))) { throw 'Query needs bounded text or role' }
        $matches=@(); $lines=@(); $visited=0
        if ($queryOnly -or (Value $argsObject 'include_ax' $true)) {
            $all=@(Nodes $targetObject.root $maximum)
            foreach ($item in $all) {
                $visited++; $attrs=Attributes $item.node; $index=Retain $item.node
                if ($queryOnly) {
                    $found=-not $needle
                    foreach ($field in @($attrs.title,$attrs.value,$attrs.identifier)) {
                        if (($exact -and [string]::Equals($field,$needle,[StringComparison]::OrdinalIgnoreCase)) -or (-not $exact -and $field.IndexOf($needle,[StringComparison]::OrdinalIgnoreCase) -ge 0)) { $found=$true }
                    }
                    if ($found -and (-not $role -or [string]::Equals($attrs.role,$role,[StringComparison]::OrdinalIgnoreCase))) {
                        $matches += @{index=$index;role=$attrs.role;title=$attrs.title;value=$attrs.value;identifier=$attrs.identifier;frame=$attrs.frame;actions=$attrs.actions;settable=$attrs.settable}
                        if ($matches.Count -ge $limit) { $script:truncated=$true; break }
                    }
                } else {
                    $summary=($attrs.title+' | '+$attrs.value).Replace("`r",'\r').Replace("`n",'\n')
                    if ($summary.Length -gt 1000) { $summary=$summary.Substring(0,1000);$script:truncated=$true }
                    $lines += ('  '*$item.depth)+"[$index] $($attrs.role) $summary settable=$($attrs.settable) actions=[$($attrs.actions -join ',')]"
                }
            }
            $metadata.truncated=$script:truncated
        }
        if ($queryOnly) { return Text @{app=$targetObject.app;pid=$targetObject.window.PID;window_id=$targetObject.window.ID;matches=@($matches);visited=$visited;truncated=$metadata.truncated} }
        $content=@()
        if (Value $argsObject 'include_screenshot' $true) {
            $png=[AIClawComputerUse.Native]::Capture($targetObject.window.ID)
            $scale=[Math]::Min(1,1280/[double][Math]::Max($targetObject.window.Frame.Right-$targetObject.window.Frame.Left,$targetObject.window.Frame.Bottom-$targetObject.window.Frame.Top))
            $metadata.image_width=[Math]::Max(1,[Math]::Round(($targetObject.window.Frame.Right-$targetObject.window.Frame.Left)*$scale))
            $metadata.image_height=[Math]::Max(1,[Math]::Round(($targetObject.window.Frame.Bottom-$targetObject.window.Frame.Top)*$scale))
            $script:imageSize=@($metadata.image_width,$metadata.image_height)
            $content += @{type='image';mimeType='image/png';data=[Convert]::ToBase64String($png)}
        }
        $content=@(@{type='text';text=(ConvertTo-Json -InputObject $metadata -Depth 8 -Compress)+"`n"+($lines -join "`n")})+$content
        return @{content=$content}
    } catch { Clear-Observation; throw }
}
function Check-Focus($targetObject) {
    Check-Cancel
    $rect=New-Object AIClawComputerUse.Native+Rect
    if ([AIClawComputerUse.Native]::GetForegroundWindow().ToInt64() -ne $targetObject.window.ID -or -not [AIClawComputerUse.Native]::GetWindowRect([IntPtr]$targetObject.window.ID,[ref]$rect) -or -not [AIClawComputerUse.Native]::SameRect($rect,$targetObject.window.Frame)) { throw 'Foreground/window changed; input may be partial, observe again' }
}
function Activate($targetObject) {
    [void][AIClawComputerUse.Native]::ShowWindow([IntPtr]$targetObject.window.ID,9)
    [void][AIClawComputerUse.Native]::SetForegroundWindow([IntPtr]$targetObject.window.ID)
    for ($i=0;$i -lt 50;$i++) {
        Check-Cancel
        if ([AIClawComputerUse.Native]::GetForegroundWindow().ToInt64() -eq $targetObject.window.ID) { Check-Focus $targetObject; return }
        [Threading.Thread]::Sleep(10)
    }
    throw 'Target did not become foreground; no global input was sent'
}
function Key-Codes([string]$key) {
    $names=@{ctrl=17;control=17;alt=18;shift=16;super=91;Return=13;Enter=13;Tab=9;Space=32;Backspace=8;Delete=46;Escape=27;Left=37;Up=38;Right=39;Down=40;Home=36;End=35;PageUp=33;PageDown=34}
    foreach ($part in $key.Split('+')) {
        if ($names.ContainsKey($part)) { [uint16]$names[$part] }
        elseif ($part -match '^[a-zA-Z0-9]$') { if ($part -cmatch '^[A-Z]$') { [uint16]16 }; [uint16][char]$part.ToUpperInvariant() }
        elseif ($part -match '^F([1-9]|1[0-9]|2[0-4])$') { [uint16](111+[int]$Matches[1]) }
        else { throw 'Unknown keyboard key or modifier' }
    }
}
function Point-From($raw, $targetObject) {
    if ($null -eq $script:imageSize -or @($raw).Count -ne 2) { throw 'Coordinates need a fresh screenshot' }
    $x=Number $raw[0] -1 0 ($script:imageSize[0]-0.001); $y=Number $raw[1] -1 0 ($script:imageSize[1]-0.001)
    $frame=Frame $targetObject.window.Frame
    return @([int][Math]::Round($frame.x+$x/$script:imageSize[0]*$frame.width),[int][Math]::Round($frame.y+$y/$script:imageSize[1]*$frame.height))
}
function Perform($argsObject) {
    if ($null -eq $script:target -or (Value $argsObject 'app') -ne $script:target.app) { throw 'Observe the target before input' }
    $previous=$script:target
    $targetObject=Resolve-Target ([pscustomobject]@{app=$previous.app;window_id=$previous.window.ID})
    if ($targetObject.window.Birth -ne $previous.window.Birth -or -not [AIClawComputerUse.Native]::SameRect($targetObject.window.Frame,$previous.window.Frame)) { Clear-Observation; throw 'Observed process or window geometry changed' }
    $entries=@($argsObject.action.PSObject.Properties)
    if ($entries.Count -ne 1) { throw 'Exactly one action is required' }
    $operation=$entries[0].Name; $values=$entries[0].Value
    $mode=Value $argsObject 'interactionMode' 'foreground'
    if ($mode -notin @('foreground','background')) { throw 'Unknown interaction mode' }
    $background=$mode -eq 'background'
    $duration=Number (Value $values 'duration') 0 0 10
    $count=[int](Number (Value $values 'clickCount') 1 1 3)
    $button=Value $values 'mouseButton' 'left'; $modifier=Value $values 'key' ''
    if ($background -and ($operation -notin @('click','setValue','selectText','performSecondaryAction') -or ($operation -eq 'click' -and ((-not (Value $values 'elementID')) -or $duration -ne 0 -or $count -ne 1 -or $button -ne 'left' -or $modifier)) -or (Value $values 'autosubmitSearchFields' $false))) { throw 'Background action unsupported; no foreground fallback or global input was sent' }
    $foreground=[AIClawComputerUse.Native]::GetForegroundWindow(); $pointer=New-Object AIClawComputerUse.Native+Point
    [void][AIClawComputerUse.Native]::GetCursorPos([ref]$pointer); $clip=[AIClawComputerUse.Native]::GetClipboardSequenceNumber()
    try {
        $semantic=$false
        if ($operation -in @('setValue','selectText','performSecondaryAction') -or ($operation -eq 'click' -and (Value $values 'elementID') -and $count -eq 1 -and $duration -eq 0 -and $button -eq 'left' -and -not $modifier)) {
            $node=Element (Value $values 'elementID') $targetObject
            if ($operation -eq 'setValue') {
                $value=Value $values 'value'
                if ($value -isnot [string] -or $value.Length -gt 2000000 -or (Value $values 'autosubmitSearchFields' $false)) { throw 'Set value needs bounded text without implicit autosubmit' }
                $pattern=Pattern $node ([System.Windows.Automation.ValuePattern]::Pattern)
                if (-not $pattern -or $pattern.Current.IsReadOnly) { throw 'Control has no writable ValuePattern' }
                if ($background) { [AIClawComputerUse.Native]::BackgroundSetValue($node.Current.NativeWindowHandle,$targetObject.window.ID,$targetObject.window.PID,$value) }
                else { $pattern.SetValue($value) }
                if ($pattern.Current.Value -ne $value) { throw 'Set value read-back differs; do not replay' }
                $semantic=$true
            } elseif ($operation -eq 'selectText') {
                if ($background) {
                    $pattern=Pattern $node ([System.Windows.Automation.ValuePattern]::Pattern)
                    if (-not $pattern) { throw 'Background selection requires a standard Edit with ValuePattern' }
                    $value=$pattern.Current.Value; $needle=Value $values 'text'
                    if ($needle -isnot [string] -or -not $needle -or $value.Length -gt 2000000) { throw 'Selection needs non-empty bounded text' }
                    $offset=0; $candidates=@()
                    while ($offset -le $value.Length) {
                        Check-Cancel; $start=$value.IndexOf($needle,$offset,[StringComparison]::Ordinal)
                        if ($start -lt 0) { break }; $end=$start+$needle.Length
                        if (((Value $values 'prefix') -eq $null -or $value.Substring(0,$start).EndsWith($values.prefix,[StringComparison]::Ordinal)) -and ((Value $values 'suffix') -eq $null -or $value.Substring($end).StartsWith($values.suffix,[StringComparison]::Ordinal))) { $candidates += ,@($start,$end) }
                        if ($candidates.Count -gt 1) { break }; $offset=$start+1
                    }
                    if ($candidates.Count -ne 1) { throw 'Selection is absent or ambiguous' }
                    $start=$candidates[0][0]; $end=$candidates[0][1]; $selection=Value $values 'selection' 'text'
                    if ($selection -eq 'cursorBefore') { $end=$start } elseif ($selection -eq 'cursorAfter') { $start=$end } elseif ($selection -ne 'text') { throw 'Unknown selection mode' }
                    [AIClawComputerUse.Native]::BackgroundSelect($node.Current.NativeWindowHandle,$targetObject.window.ID,$targetObject.window.PID,$start,$end)
                } else {
                $pattern=Pattern $node ([System.Windows.Automation.TextPattern]::Pattern)
                if (-not $pattern) { throw 'Control exposes no TextPattern selection' }
                $needle=Value $values 'text'; if ($needle -isnot [string] -or -not $needle) { throw 'Selection needs non-empty text' }
                $remaining=$pattern.DocumentRange.Clone(); $candidates=@()
                $deadline=[DateTime]::UtcNow.AddSeconds(2)
                for ($i=0;$i -lt 1000;$i++) {
                    Check-Cancel; if ([DateTime]::UtcNow -gt $deadline) { throw 'Text search exceeded its budget; no selection was applied' }
                    $found=$remaining.FindText($needle,$false,$false); if ($null -eq $found) { break }
                    $prefix=$pattern.DocumentRange.Clone(); $prefix.MoveEndpointByRange([System.Windows.Automation.TextPatternRangeEndpoint]::End,$found,[System.Windows.Automation.TextPatternRangeEndpoint]::Start)
                    $suffix=$pattern.DocumentRange.Clone(); $suffix.MoveEndpointByRange([System.Windows.Automation.TextPatternRangeEndpoint]::Start,$found,[System.Windows.Automation.TextPatternRangeEndpoint]::End)
                    if (((Value $values 'prefix') -eq $null -or $prefix.GetText(-1).EndsWith($values.prefix,[StringComparison]::Ordinal)) -and ((Value $values 'suffix') -eq $null -or $suffix.GetText(-1).StartsWith($values.suffix,[StringComparison]::Ordinal))) { $candidates += $found.Clone() }
                    $remaining.MoveEndpointByRange([System.Windows.Automation.TextPatternRangeEndpoint]::Start,$found,[System.Windows.Automation.TextPatternRangeEndpoint]::Start)
                    [void]$remaining.MoveEndpointByUnit([System.Windows.Automation.TextPatternRangeEndpoint]::Start,[System.Windows.Automation.TextUnit]::Character,1)
                }
                if ($candidates.Count -ne 1 -or $i -ge 1000) { throw 'Selection is absent, ambiguous or truncated' }
                $selected=$candidates[0]; $selection=Value $values 'selection' 'text'
                if ($selection -eq 'cursorBefore') { $selected.MoveEndpointByRange([System.Windows.Automation.TextPatternRangeEndpoint]::End,$selected,[System.Windows.Automation.TextPatternRangeEndpoint]::Start) }
                elseif ($selection -eq 'cursorAfter') { $selected.MoveEndpointByRange([System.Windows.Automation.TextPatternRangeEndpoint]::Start,$selected,[System.Windows.Automation.TextPatternRangeEndpoint]::End) }
                elseif ($selection -ne 'text') { throw 'Unknown selection mode' }
                $selected.Select(); $readback=@($pattern.GetSelection())
                if ($readback.Count -ne 1 -or $selected.CompareEndpoints([System.Windows.Automation.TextPatternRangeEndpoint]::Start,$readback[0],[System.Windows.Automation.TextPatternRangeEndpoint]::Start) -ne 0 -or $selected.CompareEndpoints([System.Windows.Automation.TextPatternRangeEndpoint]::End,$readback[0],[System.Windows.Automation.TextPatternRangeEndpoint]::End) -ne 0) { throw 'Selection could not be verified; do not replay' }
                }
                $semantic=$true
            } else {
                $requested=Value $values 'action' 'invoke'
                if ($operation -eq 'click') { $requested='invoke' }
                switch ($requested) {
                    'invoke' {
                        $pattern=Pattern $node ([System.Windows.Automation.InvokePattern]::Pattern)
                        if ($pattern) {
                            if ($background) { [AIClawComputerUse.Native]::BackgroundButton($node.Current.NativeWindowHandle,$targetObject.window.ID,$targetObject.window.PID) } else { $pattern.Invoke() }
                            $semantic=$true
                        }
                    }
                    'toggle' {
                        $pattern=Pattern $node ([System.Windows.Automation.TogglePattern]::Pattern)
                        if ($pattern) {
                            if ($background) {
                                $before=$pattern.Current.ToggleState
                                [AIClawComputerUse.Native]::BackgroundButton($node.Current.NativeWindowHandle,$targetObject.window.ID,$targetObject.window.PID)
                                if ($pattern.Current.ToggleState -eq $before) { throw 'Toggle read-back unchanged; do not replay' }
                            } else { $pattern.Toggle() }
                            $semantic=$true
                        }
                    }
                    'select' { if ($background) { throw 'Strict background UIA selection is not validated; no input dispatched' }; $pattern=Pattern $node ([System.Windows.Automation.SelectionItemPattern]::Pattern); if ($pattern) { $pattern.Select(); $semantic=$true } }
                    'expand' { if ($background) { throw 'Strict background UIA expansion is not validated; no input dispatched' }; $pattern=Pattern $node ([System.Windows.Automation.ExpandCollapsePattern]::Pattern); if ($pattern) { $pattern.Expand(); $semantic=$true } }
                    'collapse' { if ($background) { throw 'Strict background UIA collapse is not validated; no input dispatched' }; $pattern=Pattern $node ([System.Windows.Automation.ExpandCollapsePattern]::Pattern); if ($pattern) { $pattern.Collapse(); $semantic=$true } }
                    default { throw 'Requested secondary action is not exposed' }
                }
                if (-not $semantic -and ($background -or $operation -ne 'click')) { throw 'Semantic action unavailable; no global input was sent' }
            }
        }
        if (-not $semantic) {
            Activate $targetObject
            switch ($operation) {
                'click' {
                    if (Value $values 'elementID') { $bounds=(Element $values.elementID $targetObject).Current.BoundingRectangle; $point=@([int]($bounds.X+$bounds.Width/2),[int]($bounds.Y+$bounds.Height/2)) }
                    else { $point=Point-From (Value $values 'coordinate') $targetObject }
                    if ($modifier -or $button -notin @('left','right','middle')) { throw 'Unsupported click modifier or button' }
                    if (-not [AIClawComputerUse.Native]::PointBelongsTo($targetObject.window.ID,$point[0],$point[1])) { throw 'Target point is occluded by another window; no mouse input was sent' }
                    [void][AIClawComputerUse.Native]::SetCursorPos($point[0],$point[1]); $buttonCode=@{left=1;middle=2;right=3}[$button]
                    for ($i=0;$i -lt $count;$i++) { Check-Focus $targetObject; [AIClawComputerUse.Native]::Button($buttonCode,$true); try { Wait-Checked $duration } finally { [AIClawComputerUse.Native]::Button($buttonCode,$false) } }
                }
                'pressKey' {
                    $codes=@(Key-Codes (Value $values 'key' '')); $held=@()
                    try { foreach ($code in $codes) { Check-Focus $targetObject; [AIClawComputerUse.Native]::Key($code,$true);$held += $code }; Wait-Checked $duration }
                    finally { [array]::Reverse($held); foreach ($code in $held) { [AIClawComputerUse.Native]::Key($code,$false) } }
                }
                'type' {
                    $value=Value $values 'text'; if ($value -isnot [string] -or $value.Length -gt 2000000) { throw 'Type needs bounded text' }
                    foreach ($unit in $value.ToCharArray()) { Check-Focus $targetObject; [AIClawComputerUse.Native]::Unicode([uint16]$unit,$true); [AIClawComputerUse.Native]::Unicode([uint16]$unit,$false) }
                }
                'scroll' {
                    $direction=Value $values 'direction'; $pages=Number (Value $values 'pages') 1 0.01 100
                    if ($direction -notin @('up','down','left','right')) { throw 'Unknown scroll direction' }
                    $delta=[int](120*3*$pages); if ($direction -in @('down','left')) { $delta=-$delta }
                    Check-Focus $targetObject; [AIClawComputerUse.Native]::Wheel($delta,($direction -in @('left','right')))
                }
                'drag' {
                    $from=Point-From (Value $values 'from') $targetObject; $to=Point-From (Value $values 'to') $targetObject
                    if (-not [AIClawComputerUse.Native]::PointBelongsTo($targetObject.window.ID,$from[0],$from[1])) { throw 'Drag start is occluded' }
                    [void][AIClawComputerUse.Native]::SetCursorPos($from[0],$from[1]); [AIClawComputerUse.Native]::Button(1,$true)
                    try { for ($i=1;$i -le 20;$i++) { Check-Focus $targetObject; [void][AIClawComputerUse.Native]::SetCursorPos([int]($from[0]+($to[0]-$from[0])*$i/20),[int]($from[1]+($to[1]-$from[1])*$i/20));Wait-Checked 0.01 } }
                    finally { [AIClawComputerUse.Native]::Button(1,$false) }
                }
                default { throw 'Action has no validated implementation; no fallback was used' }
            }
        }
        if ($background) {
            Wait-Checked 0.05; $after=New-Object AIClawComputerUse.Native+Point; [void][AIClawComputerUse.Native]::GetCursorPos([ref]$after)
            $afterForeground=[AIClawComputerUse.Native]::GetForegroundWindow(); $afterClip=[AIClawComputerUse.Native]::GetClipboardSequenceNumber()
            if ($afterForeground -ne $foreground -or $after.X -ne $pointer.X -or $after.Y -ne $pointer.Y -or $afterClip -ne $clip) {
                throw "Background action dispatched but foreground, pointer or clipboard changed; outcome unknown, do not replay (window $foreground -> $afterForeground; pointer $($pointer.X),$($pointer.Y) -> $($after.X),$($after.Y); clipboard $clip -> $afterClip)"
            }
        }
        return Text 'Action dispatched; verify the intended application result. Previous element IDs are invalid.'
    } finally { Clear-Observation }
}
function Dispatch([string]$kind, $argsObject) {
    switch ($kind) {
        'ComputerUseIPCHandshakeRequest' { return @{protocolVersion='AIClawComputerUseIPC-1';serverVersion='1';serviceSourceSHA256=$script:sourceHash;tools=$tools} }
        'ComputerUseIPCPermissionStatusRequest' { return Text @{backend='windows-uia';service_pid=$PID;runtime_directory=$script:serviceRoot;accessibilityTrusted=$true;screenCaptureGranted=$true;uipi_restrictions=$true} }
        { $_ -in @('ComputerUseIPCListAppsRequest','ComputerUseIPCFindAppsRequest') } {
            $apps=@{}; $needle=Value $argsObject 'text' ''
            foreach ($window in [AIClawComputerUse.Native]::Windows(0)) {
                if ($window.PID -eq $PID -or $window.PID -eq $script:parentPid) { continue }
                if ($needle -and $window.Name.IndexOf($needle,[StringComparison]::OrdinalIgnoreCase) -lt 0 -and $window.Title.IndexOf($needle,[StringComparison]::OrdinalIgnoreCase) -lt 0) { continue }
                $key=[string]$window.PID
                if (-not $apps.ContainsKey($key)) { $apps[$key]=@{id=$key;pid=$window.PID;display_name=$window.Name;path=$window.Executable;is_running=$true;windows=@()} }
                $apps[$key].windows += @{window_id=$window.ID;title=$window.Title;frame=(Frame $window.Frame)}
            }
            $records=@($apps.Values)+@(Installed-Apps $needle)
            $limit=[int](Number (Value $argsObject 'limit') 100 1 100)
            return Text @{apps=@($records | Select-Object -First $limit);discovery_scope='running_and_start_menu_shortcuts';truncated=($records.Count -gt $limit)}
        }
        'ComputerUseIPCAppStartRequest' { return Start-App $argsObject }
        'ComputerUseIPCFrontmostWindowRequest' { $window=[AIClawComputerUse.Native]::GetForegroundWindow(); return Text @{app=[string][AIClawComputerUse.Native]::WindowPID($window.ToInt64());window_id=$window.ToInt64()} }
        'ComputerUseIPCAppGetSkyshotRequest' { return Observe $argsObject $false }
        'ComputerUseIPCAppQueryRequest' { return Observe $argsObject $true }
        'ComputerUseIPCAppPerformActionRequest' { return Perform $argsObject }
        'ComputerUseIPCAppModifyRequest' {
            Clear-Observation; $targetObject=Resolve-Target $argsObject; $modification=Value $argsObject 'modification'
            if ($modification -eq 'activate') { Activate $targetObject }
            elseif ($modification -eq 'deactivate') { [void][AIClawComputerUse.Native]::ShowWindow([IntPtr]$targetObject.window.ID,6) }
            else { throw 'Unknown app modification' }
            return Text @{app=$targetObject.app;modification=$modification}
        }
        'ComputerUseIPCAppStopRequest' {
            Clear-Observation; $targetObject=Resolve-Target $argsObject
            [void][AIClawComputerUse.Native]::PostMessage([IntPtr]$targetObject.window.ID,0x10,[IntPtr]::Zero,[IntPtr]::Zero); Wait-Checked 0.2
            $running=Get-Process -Id $targetObject.window.PID -ErrorAction SilentlyContinue
            return Text @{app=$targetObject.app;normal_close_requested=$true;verified=($null -eq $running)}
        }
        default { throw 'Unsupported desktop IPC request' }
    }
}
[AIClawComputerUse.Native]::BeginInput()
while (-not [AIClawComputerUse.Native]::ParentEnded) {
    $line=$null
    if (-not [AIClawComputerUse.Native]::ReadFrame([ref]$line)) { [Threading.Thread]::Sleep(10); continue }
    $frame=$null
    try { $frame=ConvertFrom-Json -InputObject $line; $result=Dispatch $frame.requestType $frame.request; $response=@{id=$frame.id;result=$result} }
    catch { $response=@{id=$(if ($frame) {$frame.id} else {$null});error=@{message=$_.Exception.Message}} }
    [Console]::WriteLine((ConvertTo-Json -InputObject $response -Depth 40 -Compress))
}
