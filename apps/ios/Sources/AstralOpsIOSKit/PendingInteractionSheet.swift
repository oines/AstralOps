import SwiftUI

struct PendingInteractionSheet: View {
    @EnvironmentObject private var model: AppModel
    let interaction: PendingInteractionView
    @State private var feedback = ""
    @State private var textAnswers: [String: String] = [:]
    @State private var selectedAnswers: [String: Set<String>] = [:]
    @State private var mcpContent = "{}"

    private var form: InteractionForm {
        InteractionForm(interaction.form)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    header
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())

                if let rows = interaction.detailRows, !rows.isEmpty {
                    Section("Details") {
                        ForEach(rows) { row in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(row.key ?? row.label)
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                Text(row.value)
                                    .font(row.mono == true ? .system(.callout, design: .monospaced) : .callout)
                                    .textSelection(.enabled)
                            }
                        }
                    }
                }

                if form.kind != "none" {
                    Section("Response") {
                        formBody

                        if form.kind != "mcp_url" {
                            TextField("Feedback", text: $feedback, axis: .vertical)
                                .lineLimit(2...5)
                        }
                    }
                }

                Section {
                    ForEach(interaction.actions) { action in
                        Button(role: action.role == "danger" || action.role == "destructive" ? .destructive : nil) {
                            Task { await submit(action) }
                        } label: {
                            HStack {
                                Text(action.label)
                                    .font(.body.weight(.semibold))
                                Spacer()
                                Image(systemName: "arrow.right")
                            }
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(action.role == "danger" || action.role == "destructive" ? .red : nil)
                    }
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            }
            .navigationTitle("Action Required")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") {
                        model.pendingInteraction = nil
                    }
                }
            }
            .onAppear {
                initializeForm()
            }
        }
    }


    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(interaction.kind.uppercased())
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(interaction.title)
                .font(.title3.weight(.semibold))
        }
        .padding(.horizontal)
        .padding(.top, 8)
    }

    @ViewBuilder
    private var formBody: some View {
        switch form.kind {
        case "mcp_url":
            VStack(alignment: .leading, spacing: 8) {
                if !form.message.isEmpty {
                    Text(form.message)
                        .font(.callout)
                }
                if !form.url.isEmpty {
                    Link(form.url, destination: URL(string: form.url) ?? URL(fileURLWithPath: "/"))
                        .font(.callout.weight(.semibold))
                }
            }
            .padding(12)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        case "mcp_json":
            VStack(alignment: .leading, spacing: 8) {
                if !form.message.isEmpty {
                    Text(form.message)
                        .font(.callout)
                }
                TextField("JSON content", text: $mcpContent, axis: .vertical)
                    .font(.system(.callout, design: .monospaced))
                    .lineLimit(4...12)
                    .textFieldStyle(.roundedBorder)
            }
        case "questions":
            VStack(spacing: 14) {
                ForEach(form.fields) { field in
                    questionField(field)
                }
            }
        case "text":
            TextField("Answer", text: binding(for: "question_0"), axis: .vertical)
                .lineLimit(2...6)
                .textFieldStyle(.roundedBorder)
        default:
            EmptyView()
        }
    }

    @ViewBuilder
    private func questionField(_ field: InteractionField) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(field.label)
                .font(.callout.weight(.semibold))
            if !field.description.isEmpty {
                Text(field.description)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if field.type == "choice", !field.options.isEmpty {
                if field.multiSelect {
                    ForEach(field.options) { option in
                        Toggle(isOn: multiSelectBinding(fieldID: field.id, value: option.value)) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(option.label)
                                if !option.description.isEmpty {
                                    Text(option.description)
                                        .font(.footnote)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                } else {
                    Picker(field.label, selection: binding(for: field.id)) {
                        ForEach(field.options) { option in
                            Text(option.label).tag(option.value)
                        }
                    }
                    .pickerStyle(.inline)
                }
                if field.allowCustom {
                    TextField("Custom answer", text: binding(for: "\(field.id)__custom"), axis: .vertical)
                        .lineLimit(1...4)
                        .textFieldStyle(.roundedBorder)
                }
            } else {
                if field.secret {
                    SecureField("Answer", text: binding(for: field.id))
                        .textFieldStyle(.roundedBorder)
                } else {
                    TextField("Answer", text: binding(for: field.id), axis: .vertical)
                        .lineLimit(1...5)
                        .textFieldStyle(.roundedBorder)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func initializeForm() {
        if form.kind == "mcp_json", !form.initialContent.isEmpty {
            mcpContent = form.initialContent
        }
        for field in form.fields where field.type == "choice" && !field.multiSelect && textAnswers[field.id] == nil {
            textAnswers[field.id] = field.options.first?.value ?? ""
        }
    }

    private func submit(_ action: InteractionActionView) async {
        var payload: [String: JSONValue] = ["action_id": .string(action.id)]
        if !feedback.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            payload["feedback"] = .string(feedback)
        }
        switch form.kind {
        case "mcp_json":
            payload["content"] = parseJSONContent(mcpContent)
        case "questions":
            payload["answers"] = .object(questionAnswers())
        case "text":
            let answer = textAnswers["question_0"]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !answer.isEmpty {
                payload["text"] = .string(answer)
            }
        default:
            break
        }
        await model.respond(to: interaction, response: .object(payload))
    }

    private func questionAnswers() -> [String: JSONValue] {
        var answers: [String: JSONValue] = [:]
        for field in form.fields {
            if field.multiSelect {
                var values = Array(selectedAnswers[field.id] ?? []).sorted()
                let custom = textAnswers["\(field.id)__custom"]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if field.allowCustom, !custom.isEmpty {
                    values.append(custom)
                }
                if !values.isEmpty {
                    answers[field.id] = .array(values.map { .string($0) })
                }
            } else {
                var value = textAnswers[field.id]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                let custom = textAnswers["\(field.id)__custom"]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if field.allowCustom, !custom.isEmpty {
                    value = custom
                }
                if !value.isEmpty {
                    answers[field.id] = .string(value)
                }
            }
        }
        return answers
    }

    private func parseJSONContent(_ value: String) -> JSONValue {
        guard let data = value.data(using: .utf8),
              let parsed = try? JSONCoding.decode(JSONValue.self, from: data)
        else {
            return .object([:])
        }
        return parsed
    }

    private func binding(for key: String) -> Binding<String> {
        Binding(
            get: { textAnswers[key] ?? "" },
            set: { textAnswers[key] = $0 }
        )
    }

    private func multiSelectBinding(fieldID: String, value: String) -> Binding<Bool> {
        Binding(
            get: { selectedAnswers[fieldID, default: []].contains(value) },
            set: { enabled in
                var values = selectedAnswers[fieldID, default: []]
                if enabled {
                    values.insert(value)
                } else {
                    values.remove(value)
                }
                selectedAnswers[fieldID] = values
            }
        )
    }
}

private struct InteractionForm {
    var kind = ""
    var message = ""
    var url = ""
    var initialContent = "{}"
    var fields: [InteractionField] = []

    init(_ value: JSONValue?) {
        guard let object = value?.objectValue else { return }
        kind = object["kind"]?.stringValue ?? ""
        message = object["message"]?.stringValue ?? ""
        url = object["url"]?.stringValue ?? ""
        initialContent = object["initial_content"]?.stringValue ?? "{}"
        fields = (object["fields"]?.arrayValue ?? []).map(InteractionField.init).filter { !$0.id.isEmpty }
    }
}

private struct InteractionField: Identifiable {
    var id = ""
    var label = ""
    var description = ""
    var type = "text"
    var options: [InteractionOption] = []
    var multiSelect = false
    var allowCustom = false
    var secret = false

    init(_ value: JSONValue) {
        let object = value.objectValue ?? [:]
        id = object["id"]?.stringValue ?? ""
        label = object["label"]?.stringValue ?? id
        description = object["description"]?.stringValue ?? ""
        type = object["type"]?.stringValue ?? "text"
        options = (object["options"]?.arrayValue ?? []).map(InteractionOption.init).filter { !$0.value.isEmpty }
        multiSelect = object["multi_select"]?.boolValue ?? false
        allowCustom = object["allow_custom"]?.boolValue ?? false
        secret = object["secret"]?.boolValue ?? false
    }
}

private struct InteractionOption: Identifiable {
    var id = ""
    var label = ""
    var value = ""
    var description = ""

    init(_ value: JSONValue) {
        let object = value.objectValue ?? [:]
        self.id = object["id"]?.stringValue ?? object["value"]?.stringValue ?? ""
        self.value = object["value"]?.stringValue ?? object["id"]?.stringValue ?? ""
        self.label = object["label"]?.stringValue ?? self.value
        self.description = object["description"]?.stringValue ?? ""
    }
}
