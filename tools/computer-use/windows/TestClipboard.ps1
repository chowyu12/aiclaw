$ErrorActionPreference='Stop'
$root=$PSScriptRoot
Add-Type -AssemblyName UIAutomationClient,System.Drawing,System.Windows.Forms
Add-Type -Path @((Join-Path $root 'Native.cs'),(Join-Path $root 'Clipboard.cs'),(Join-Path $root 'TestClipboard.cs')) -ReferencedAssemblies @('System.dll','System.Core.dll','System.Drawing.dll','System.Windows.Forms.dll',[System.Windows.Automation.AutomationElement].Assembly.Location)
[TestClipboard]::Run()
