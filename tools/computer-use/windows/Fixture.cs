// Private acceptance app; never reads user files or networks.
using System;
using System.Collections.Generic;
using System.Drawing;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;
using System.Windows.Forms;

public class FixturePad : Panel {
    public int Drags, Scrolls;
    public FixturePad() { SetStyle(ControlStyles.Selectable, true); TabStop = true; BackColor = Color.White; }
    protected override void OnMouseDown(MouseEventArgs e) { base.OnMouseDown(e); Capture = true; }
    protected override void OnMouseUp(MouseEventArgs e) { base.OnMouseUp(e); Capture = false; }
    protected override void OnMouseMove(MouseEventArgs e) { base.OnMouseMove(e); if (e.Button == MouseButtons.Left) Drags++; }
    protected override void OnMouseEnter(EventArgs e) { base.OnMouseEnter(e); Focus(); }
    protected override void OnMouseWheel(MouseEventArgs e) { base.OnMouseWheel(e); Scrolls++; }
}
public static class FixtureProgram {
    [STAThread] public static void Main(string[] args) {
        Application.EnableVisualStyles();
        var form = new Form { Text = args[1], StartPosition = FormStartPosition.Manual, Location = new Point(100, 100), ClientSize = new Size(420, 480), BackColor = args[1].Contains("Target") ? Color.FromArgb(204,51,68) : Color.FromArgb(51,85,204) };
        var input = new TextBox { Bounds = new Rectangle(20,20,370,28), AccessibleName = "Fixture input", Name = "fixture-input" };
        var button = new Button { Bounds = new Rectangle(20,64,180,30), Text = "Submit fixture", AccessibleName = "Submit fixture" };
        var toggle = new CheckBox { Bounds = new Rectangle(220,64,170,30), Text = "Fixture toggle", AccessibleName = "Fixture toggle" };
        var status = new Label { Bounds = new Rectangle(20,106,370,35), Text = "No input submitted" };
        var pad = new FixturePad { Bounds = new Rectangle(20,150,370,110), AccessibleName = "Fixture gesture pad" };
        var rich = new RichTextBox { Bounds = new Rectangle(20,290,370,140), AccessibleName = "Fixture rich editor", Name = "fixture-rich" };
        button.Click += (sender, e) => {
            status.Text = "Submitted: " + input.Text;
            var values = new Dictionary<string,object> { {"value", input.Text}, {"checked", toggle.Checked}, {"drags",pad.Drags}, {"scrolls",pad.Scrolls}, {"rich",rich.Text} };
            File.WriteAllText(args[0], new JavaScriptSerializer().Serialize(values), new UTF8Encoding(false));
        };
        foreach (Control control in new Control[] { input, button, toggle, status, pad, rich }) form.Controls.Add(control);
        form.Shown += (sender, e) => { input.Focus(); File.WriteAllText(args[0]+".ready", "ready"); };
        Application.Run(form);
    }
}
