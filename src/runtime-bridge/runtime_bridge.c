#define WIN32_LEAN_AND_MEAN
#define _WIN32_WINNT 0x0601

#include <windows.h>
#include <dxgi.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARRAY_COUNT(value) (sizeof(value) / sizeof((value)[0]))

/*
 * These RVAs are generated from the matching MORDHAU Dedicated Server PDB.
 * The bridge refuses to install its tick hook unless the expected prologue
 * bytes also match.
 */
enum {
    RVA_UWORLD_TICK = 0x02D80E40,
    RVA_GWORLD = 0x05374F88,
    RVA_GUOBJECT_ARRAY = 0x0524C728,
    RVA_FNAME_TO_STRING = 0x01854310,
    RVA_FSTRING_DESTRUCTOR = 0x0077CBC0,
    RVA_FPROPERTY_IMPORT_TEXT = 0x01299EF0,
    RVA_AACTOR_FLUSH_NET_DORMANCY = 0x02A24C30,
    RVA_AACTOR_FORCE_NET_UPDATE = 0x02A25190,
    RVA_MORDHAU_INVENTORY_GET_PLAYER_XP = 0x015202F0,
    RVA_MORDHAU_INVENTORY_IS_AVAILABLE = 0x01525940,
    RVA_MORDHAU_UTILITY_GET_INVENTORY = 0x01569210,
    RVA_MORDHAU_UTILITY_GET_RANK_FROM_XP = 0x0156D620,
    RVA_GWARN = 0x05201A80,
    UWORLD_AUTHORITY_GAME_MODE = 0x0118,
    UWORLD_GAME_STATE = 0x0120,
    UWORLD_PLAYER_CONTROLLER_LIST = 0x01C0,
    UOBJECT_INTERNAL_INDEX = 0x000C,
    UOBJECT_CLASS_PRIVATE = 0x0010,
    UOBJECT_NAME_PRIVATE = 0x0018,
    USTRUCT_SUPER_STRUCT = 0x0040,
    USTRUCT_CHILD_PROPERTIES = 0x0050,
    USTRUCT_PROPERTIES_SIZE = 0x0058,
    FFIELD_CLASS_PRIVATE = 0x0008,
    FFIELD_NEXT = 0x0020,
    FFIELD_NAME_PRIVATE = 0x0028,
    FPROPERTY_ARRAY_DIM = 0x0038,
    FPROPERTY_ELEMENT_SIZE = 0x003C,
    FPROPERTY_FLAGS = 0x0040,
    FPROPERTY_REP_INDEX = 0x0048,
    FPROPERTY_REP_CONDITION = 0x004A,
    FPROPERTY_OFFSET_INTERNAL = 0x004C,
    FPROPERTY_REP_NOTIFY_FUNC = 0x0050,
    FPROPERTY_VTABLE_EXPORT_TEXT_ITEM = 21,
    FBYTEPROPERTY_ENUM = 0x0078,
    FENUMPROPERTY_ENUM = 0x0080,
    UENUM_NAMES_DATA = 0x0040,
    UENUM_NAMES_COUNT = 0x0048,
    UENUM_NAMES_CAPACITY = 0x004C,
    UENUM_NAME_VALUE_SIZE = 16,
    OBJECTS_PER_CHUNK = 65536,
    UOBJECT_ITEM_SIZE = 24,
    MAX_RUNTIME_TARGETS = 3072,
    MAX_RESPONSE_BYTES = 8 * 1024 * 1024,
    MAX_STATUS_BYTES = 2 * 1024 * 1024,
    MAX_REQUEST_BYTES = 512 * 1024,
    MAX_TEXT_VALUE_BYTES = 120 * 1024,
    MAX_PROPERTIES_PER_TARGET = 8192,
    MAX_ENUM_VALUES = 1024,
};

#define CPF_NET UINT64_C(32)
#define CPF_PARM UINT64_C(128)
#define CPF_OUT_PARM UINT64_C(256)
#define CPF_RETURN_PARM UINT64_C(1024)
#define CPF_DEPRECATED UINT64_C(536870912)
#define CPF_REP_SKIP UINT64_C(2147483648)
#define CPF_REP_NOTIFY UINT64_C(4294967296)
#define CPF_EDITOR_ONLY UINT64_C(34359738368)

typedef HRESULT (WINAPI *CreateDXGIFactoryFn)(REFIID riid, void **factory);
typedef void (__fastcall *UWorldTickFn)(void *world, uint8_t tick_type, float delta_seconds);
typedef struct {
    int32_t comparison_index;
    int32_t number;
} FName;
typedef struct {
    wchar_t *data;
    int32_t count;
    int32_t capacity;
} FString;
typedef void (__fastcall *FNameToStringFn)(const FName *name, FString *out);
typedef void (__fastcall *FStringDestructorFn)(FString *value);
typedef void (__fastcall *FPropertyExportTextItemFn)(
    const void *property,
    FString *out,
    const void *value,
    const void *default_value,
    void *parent,
    int32_t port_flags,
    void *export_root_scope
);
typedef const wchar_t *(__fastcall *FPropertyImportTextFn)(
    const void *property,
    const wchar_t *text,
    void *value,
    int32_t port_flags,
    void *owner,
    void *error_output
);
typedef void (__fastcall *AActorFlushNetDormancyFn)(void *actor);
typedef void (__fastcall *AActorForceNetUpdateFn)(void *actor);
typedef int (__fastcall *MordhauInventoryGetPlayerXPFn)(
    void *inventory,
    const FString *playfab_id
);
typedef uint8_t (__fastcall *MordhauInventoryIsAvailableFn)(
    void *inventory,
    const FString *playfab_id
);
typedef void *(__fastcall *MordhauUtilityGetInventoryFn)(void);
typedef int (__fastcall *MordhauUtilityGetRankFromXPFn)(int xp);

typedef struct {
    int32_t object_index;
    int32_t object_serial;
} WeakObjectPtr;

typedef struct {
    WeakObjectPtr *data;
    int32_t count;
    int32_t capacity;
} WeakObjectArray;

typedef struct {
    char *data;
    size_t length;
    size_t capacity;
    int failed;
} JsonBuilder;

typedef enum {
    BRIDGE_COMMAND_NONE = 0,
    BRIDGE_COMMAND_STATUS,
    BRIDGE_COMMAND_GET,
    BRIDGE_COMMAND_SET,
} BridgeCommand;

typedef struct {
    BridgeCommand command;
    char request_id[65];
    char target_id[128];
    char declaring_class[256];
    char property_name[256];
    int array_index;
    char expected_value[MAX_TEXT_VALUE_BYTES];
    char new_value[MAX_TEXT_VALUE_BYTES];
} BridgeRequest;

typedef struct {
    void *object;
    char id[128];
    char kind[32];
    char class_name[192];
    int player_slot;
    char player_name[512];
    char playfab_id[128];
    char platform[32];
    char platform_account_id[128];
    int ping_ms;
    int has_ping;
} RuntimeTarget;

static INIT_ONCE g_proxy_once = INIT_ONCE_STATIC_INIT;
static INIT_ONCE g_bridge_once = INIT_ONCE_STATIC_INIT;
static HMODULE g_real_dxgi;
static CreateDXGIFactoryFn g_real_create_factory;
static uint8_t *g_image_base;
static UWorldTickFn g_original_world_tick;
static FNameToStringFn g_fname_to_string;
static FStringDestructorFn g_fstring_destructor;
static FPropertyImportTextFn g_property_import_text;
static AActorFlushNetDormancyFn g_flush_net_dormancy;
static AActorForceNetUpdateFn g_force_net_update;
static MordhauInventoryGetPlayerXPFn g_inventory_get_player_xp;
static MordhauInventoryIsAvailableFn g_inventory_is_available;
static MordhauUtilityGetInventoryFn g_utility_get_inventory;
static MordhauUtilityGetRankFromXPFn g_utility_get_rank_from_xp;
static ULONGLONG g_last_sample_ms;
static HANDLE g_response_event;
static volatile LONG g_request_state;
static volatile LONG g_status_state;
static BridgeRequest g_request;
static char g_response_buffer[MAX_RESPONSE_BYTES];
static char g_status_buffer[MAX_STATUS_BYTES];
static size_t g_response_length;
static size_t g_status_length;
static RuntimeTarget g_targets[MAX_RUNTIME_TARGETS];
static char g_value_buffer[MAX_TEXT_VALUE_BYTES];
static char g_secondary_value_buffer[MAX_TEXT_VALUE_BYTES];

static const wchar_t *const g_log_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\runtime-bridge.log";
static const wchar_t *const g_status_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\runtime-bridge-status.json";
static const wchar_t *const g_status_temp_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\.runtime-bridge-status.tmp";
static const wchar_t *const g_request_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\runtime-bridge-request.txt";
static const wchar_t *const g_response_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\runtime-bridge-response.json";
static const wchar_t *const g_response_temp_path =
    L"Z:\\root\\mordhau\\.manager\\runtime\\.runtime-bridge-response.tmp";

static void append_log(const char *message)
{
    HANDLE file;
    DWORD written;
    SYSTEMTIME now;
    char line[1024];
    int length;

    GetLocalTime(&now);
    length = snprintf(
        line,
        sizeof(line),
        "%04u-%02u-%02u %02u:%02u:%02u.%03u %s\r\n",
        (unsigned)now.wYear,
        (unsigned)now.wMonth,
        (unsigned)now.wDay,
        (unsigned)now.wHour,
        (unsigned)now.wMinute,
        (unsigned)now.wSecond,
        (unsigned)now.wMilliseconds,
        message
    );
    if (length <= 0) {
        return;
    }
    if ((size_t)length >= sizeof(line)) {
        length = (int)sizeof(line) - 1;
    }

    file = CreateFileW(
        g_log_path,
        FILE_APPEND_DATA,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
        NULL,
        OPEN_ALWAYS,
        FILE_ATTRIBUTE_NORMAL,
        NULL
    );
    if (file == INVALID_HANDLE_VALUE) {
        return;
    }
    WriteFile(file, line, (DWORD)length, &written, NULL);
    CloseHandle(file);
}

static void json_init(JsonBuilder *builder, char *data, size_t capacity)
{
    if (builder == NULL) {
        return;
    }
    builder->data = data;
    builder->length = 0;
    builder->capacity = capacity;
    builder->failed = data == NULL || capacity < 2;
    if (!builder->failed) {
        data[0] = '\0';
    }
}

static void json_append_bytes(
    JsonBuilder *builder,
    const char *value,
    size_t value_length
)
{
    if (builder == NULL || builder->failed ||
        value == NULL ||
        value_length >= builder->capacity - builder->length) {
        if (builder != NULL) {
            builder->failed = 1;
        }
        return;
    }
    memcpy(builder->data + builder->length, value, value_length);
    builder->length += value_length;
    builder->data[builder->length] = '\0';
}

static void json_append(JsonBuilder *builder, const char *value)
{
    if (value == NULL) {
        if (builder != NULL) {
            builder->failed = 1;
        }
        return;
    }
    json_append_bytes(builder, value, strlen(value));
}

static void json_append_format(JsonBuilder *builder, const char *format, ...)
{
    va_list arguments;
    int length;
    size_t remaining;

    if (builder == NULL || builder->failed || format == NULL) {
        return;
    }
    remaining = builder->capacity - builder->length;
    va_start(arguments, format);
    length = vsnprintf(
        builder->data + builder->length,
        remaining,
        format,
        arguments
    );
    va_end(arguments);
    if (length < 0 || (size_t)length >= remaining) {
        builder->failed = 1;
        return;
    }
    builder->length += (size_t)length;
}

static void json_append_string(JsonBuilder *builder, const char *value)
{
    const unsigned char *cursor;
    const unsigned char *run;
    char escaped[7];

    if (builder == NULL || builder->failed || value == NULL) {
        if (builder != NULL) {
            builder->failed = 1;
        }
        return;
    }
    json_append_bytes(builder, "\"", 1);
    cursor = (const unsigned char *)value;
    run = cursor;
    while (*cursor != '\0') {
        if (*cursor == '"' || *cursor == '\\' || *cursor < 0x20) {
            json_append_bytes(
                builder,
                (const char *)run,
                (size_t)(cursor - run)
            );
            if (*cursor == '"' || *cursor == '\\') {
                escaped[0] = '\\';
                escaped[1] = (char)*cursor;
                json_append_bytes(builder, escaped, 2);
            } else {
                snprintf(escaped, sizeof(escaped), "\\u%04x", *cursor);
                json_append_bytes(builder, escaped, 6);
            }
            ++cursor;
            run = cursor;
            continue;
        }
        ++cursor;
    }
    json_append_bytes(builder, (const char *)run, (size_t)(cursor - run));
    json_append_bytes(builder, "\"", 1);
}

static int write_atomic_file(
    const wchar_t *temporary_path,
    const wchar_t *destination_path,
    const char *data,
    size_t data_length
)
{
    HANDLE file;
    DWORD written;

    if (temporary_path == NULL || destination_path == NULL || data == NULL ||
        data_length > UINT32_MAX) {
        return 0;
    }
    file = CreateFileW(
        temporary_path,
        GENERIC_WRITE,
        FILE_SHARE_READ,
        NULL,
        CREATE_ALWAYS,
        FILE_ATTRIBUTE_NORMAL,
        NULL
    );
    if (file == INVALID_HANDLE_VALUE) {
        return 0;
    }
    if (!WriteFile(file, data, (DWORD)data_length, &written, NULL) ||
        written != (DWORD)data_length ||
        !FlushFileBuffers(file)) {
        CloseHandle(file);
        DeleteFileW(temporary_path);
        return 0;
    }
    CloseHandle(file);
    if (!MoveFileExW(
            temporary_path,
            destination_path,
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary_path);
        return 0;
    }
    return 1;
}

static int hex_digit_value(char value)
{
    if (value >= '0' && value <= '9') {
        return value - '0';
    }
    if (value >= 'a' && value <= 'f') {
        return value - 'a' + 10;
    }
    if (value >= 'A' && value <= 'F') {
        return value - 'A' + 10;
    }
    return -1;
}

static int decode_hex(
    const char *encoded,
    char *decoded,
    size_t decoded_capacity
)
{
    size_t encoded_length;
    size_t index;

    if (encoded == NULL || decoded == NULL || decoded_capacity < 1) {
        return 0;
    }
    encoded_length = strlen(encoded);
    if ((encoded_length & 1U) != 0 ||
        encoded_length / 2 >= decoded_capacity) {
        return 0;
    }
    for (index = 0; index < encoded_length; index += 2) {
        int high = hex_digit_value(encoded[index]);
        int low = hex_digit_value(encoded[index + 1]);
        if (high < 0 || low < 0) {
            return 0;
        }
        decoded[index / 2] = (char)((high << 4) | low);
    }
    decoded[encoded_length / 2] = '\0';
    return 1;
}

static int request_id_is_valid(const char *request_id)
{
    const unsigned char *cursor = (const unsigned char *)request_id;
    size_t length = 0;

    if (cursor == NULL) {
        return 0;
    }
    while (*cursor != '\0') {
        if (!( (*cursor >= 'a' && *cursor <= 'z') ||
               (*cursor >= 'A' && *cursor <= 'Z') ||
               (*cursor >= '0' && *cursor <= '9') ||
               *cursor == '-' || *cursor == '_' )) {
            return 0;
        }
        ++cursor;
        if (++length > 64) {
            return 0;
        }
    }
    return length > 0;
}

static BOOL CALLBACK initialize_proxy(PINIT_ONCE once, PVOID parameter, PVOID *context)
{
    wchar_t path[MAX_PATH + 16];
    UINT length;

    (void)once;
    (void)parameter;
    (void)context;

    length = GetSystemDirectoryW(path, MAX_PATH);
    if (length == 0 || length >= MAX_PATH - 10) {
        return FALSE;
    }
    memcpy(path + length, L"\\dxgi.dll", sizeof(L"\\dxgi.dll"));

    g_real_dxgi = LoadLibraryW(path);
    if (g_real_dxgi == NULL) {
        return FALSE;
    }
    g_real_create_factory = (CreateDXGIFactoryFn)(uintptr_t)GetProcAddress(
        g_real_dxgi,
        "CreateDXGIFactory"
    );
    return g_real_create_factory != NULL;
}

static void *resolve_weak_object(const WeakObjectPtr *weak)
{
    uint8_t *global;
    uint8_t ***chunks_address;
    uint8_t **chunks;
    uint8_t *chunk;
    uint8_t *item;
    int32_t num_elements;
    int32_t item_serial;
    void *object;

    if (weak == NULL || weak->object_index < 0 || weak->object_serial <= 0) {
        return NULL;
    }

    global = g_image_base + RVA_GUOBJECT_ARRAY;
    num_elements = *(int32_t *)(global + 0x24);
    if (num_elements < 1 || num_elements > 4000000 ||
        weak->object_index >= num_elements) {
        return NULL;
    }

    chunks_address = (uint8_t ***)(global + 0x10);
    chunks = *chunks_address;
    if (chunks == NULL) {
        return NULL;
    }
    chunk = chunks[weak->object_index / OBJECTS_PER_CHUNK];
    if (chunk == NULL) {
        return NULL;
    }
    item = chunk +
        ((size_t)(weak->object_index % OBJECTS_PER_CHUNK) * UOBJECT_ITEM_SIZE);
    object = *(void **)item;
    item_serial = *(int32_t *)(item + 0x10);
    if (object == NULL || item_serial != weak->object_serial) {
        return NULL;
    }
    return object;
}

static int fname_to_utf8(const FName *name, char *out, size_t out_capacity)
{
    FString value = {0};
    int wide_length;
    int result;

    if (name == NULL || out == NULL || out_capacity < 2 ||
        g_fname_to_string == NULL || g_fstring_destructor == NULL) {
        return 0;
    }
    g_fname_to_string(name, &value);
    if (value.data == NULL || value.count <= 0 ||
        value.count > 1048576 || value.capacity < value.count) {
        if (value.data != NULL) {
            g_fstring_destructor(&value);
        }
        return 0;
    }
    wide_length = value.count;
    if (wide_length > 0 && value.data[wide_length - 1] == L'\0') {
        --wide_length;
    }
    result = WideCharToMultiByte(
        CP_UTF8,
        WC_ERR_INVALID_CHARS,
        value.data,
        wide_length,
        out,
        (int)out_capacity - 1,
        NULL,
        NULL
    );
    g_fstring_destructor(&value);
    if (result <= 0) {
        out[0] = '\0';
        return 0;
    }
    out[result] = '\0';
    return result;
}

static int count_class_properties(void *object)
{
    uint8_t *class_object;
    int count = 0;
    int class_depth = 0;

    if (object == NULL) {
        return 0;
    }
    class_object = *(uint8_t **)((uint8_t *)object + UOBJECT_CLASS_PRIVATE);
    while (class_object != NULL && class_depth++ < 128) {
        uint8_t *field = *(uint8_t **)(class_object + USTRUCT_CHILD_PROPERTIES);
        int field_count = 0;
        while (field != NULL && field_count++ < 8192) {
            ++count;
            field = *(uint8_t **)(field + FFIELD_NEXT);
        }
        class_object = *(uint8_t **)(class_object + USTRUCT_SUPER_STRUCT);
    }
    return count;
}

static uint8_t *find_property(void *object, const char *wanted_name)
{
    uint8_t *class_object;
    int class_depth = 0;
    char name[256];

    if (object == NULL || wanted_name == NULL) {
        return NULL;
    }
    class_object = *(uint8_t **)((uint8_t *)object + UOBJECT_CLASS_PRIVATE);
    while (class_object != NULL && class_depth++ < 128) {
        uint8_t *field = *(uint8_t **)(class_object + USTRUCT_CHILD_PROPERTIES);
        int field_count = 0;
        while (field != NULL && field_count++ < 8192) {
            if (fname_to_utf8(
                    (const FName *)(field + FFIELD_NAME_PRIVATE),
                    name,
                    sizeof(name)) > 0 &&
                strcmp(name, wanted_name) == 0) {
                return field;
            }
            field = *(uint8_t **)(field + FFIELD_NEXT);
        }
        class_object = *(uint8_t **)(class_object + USTRUCT_SUPER_STRUCT);
    }
    return NULL;
}

static int export_property_utf8(
    uint8_t *property,
    void *object,
    int array_index,
    char *out,
    size_t out_capacity
)
{
    FString value = {0};
    FPropertyExportTextItemFn export_text_item;
    int32_t array_dim;
    int32_t element_size;
    int32_t offset;
    int wide_length;
    int result;

    if (property == NULL || object == NULL || out == NULL || out_capacity < 2 ||
        g_fstring_destructor == NULL) {
        return 0;
    }
    array_dim = *(int32_t *)(property + FPROPERTY_ARRAY_DIM);
    element_size = *(int32_t *)(property + FPROPERTY_ELEMENT_SIZE);
    offset = *(int32_t *)(property + FPROPERTY_OFFSET_INTERNAL);
    if (array_dim < 1 || array_dim > 1024 ||
        element_size < 1 || element_size > 0x1000000 ||
        array_index < 0 || array_index >= array_dim ||
        offset < 0 || offset > 0x1000000 ||
        (uint64_t)(uint32_t)offset +
            ((uint64_t)(uint32_t)element_size * (uint32_t)array_index) >
            UINT32_MAX) {
        return 0;
    }
    export_text_item = ((FPropertyExportTextItemFn *)(*(void ***)property))[
        FPROPERTY_VTABLE_EXPORT_TEXT_ITEM
    ];
    if (export_text_item == NULL) {
        return 0;
    }
    export_text_item(
        property,
        &value,
        (uint8_t *)object + offset + ((size_t)element_size * (size_t)array_index),
        NULL,
        object,
        0,
        object
    );
    if (value.count < 0 ||
        value.count > 1048576 || value.capacity < value.count) {
        if (value.data != NULL) {
            g_fstring_destructor(&value);
        }
        return 0;
    }
    wide_length = value.count;
    if (wide_length > 0 && value.data[wide_length - 1] == L'\0') {
        --wide_length;
    }
    if (wide_length == 0) {
        out[0] = '\0';
        if (value.data != NULL) {
            g_fstring_destructor(&value);
        }
        return 1;
    }
    if (value.data == NULL) {
        return 0;
    }
    result = WideCharToMultiByte(
        CP_UTF8,
        WC_ERR_INVALID_CHARS,
        value.data,
        wide_length,
        out,
        (int)out_capacity - 1,
        NULL,
        NULL
    );
    g_fstring_destructor(&value);
    if (result <= 0) {
        out[0] = '\0';
        return 0;
    }
    out[result] = '\0';
    return 1;
}

static int get_object_identity(
    void *object,
    int32_t *object_index,
    int32_t *object_serial
)
{
    uint8_t *global;
    uint8_t **chunks;
    uint8_t *chunk;
    uint8_t *item;
    int32_t index;
    int32_t num_elements;
    int32_t serial;

    if (object == NULL || object_index == NULL || object_serial == NULL ||
        g_image_base == NULL) {
        return 0;
    }
    index = *(int32_t *)((uint8_t *)object + UOBJECT_INTERNAL_INDEX);
    global = g_image_base + RVA_GUOBJECT_ARRAY;
    num_elements = *(int32_t *)(global + 0x24);
    if (index < 0 || index >= num_elements || num_elements > 4000000) {
        return 0;
    }
    chunks = *(uint8_t ***)(global + 0x10);
    if (chunks == NULL) {
        return 0;
    }
    chunk = chunks[index / OBJECTS_PER_CHUNK];
    if (chunk == NULL) {
        return 0;
    }
    item = chunk + ((size_t)(index % OBJECTS_PER_CHUNK) * UOBJECT_ITEM_SIZE);
    serial = *(int32_t *)(item + 0x10);
    if (*(void **)item != object || serial <= 0) {
        return 0;
    }
    *object_index = index;
    *object_serial = serial;
    return 1;
}

static int is_registered_uobject(void *object)
{
    uint8_t *global;
    uint8_t **chunks;
    uint8_t *chunk;
    uint8_t *item;
    int32_t index;
    int32_t num_elements;

    if (object == NULL || g_image_base == NULL) {
        return 0;
    }
    index = *(int32_t *)((uint8_t *)object + UOBJECT_INTERNAL_INDEX);
    global = g_image_base + RVA_GUOBJECT_ARRAY;
    num_elements = *(int32_t *)(global + 0x24);
    if (index < 0 || index >= num_elements || num_elements > 4000000) {
        return 0;
    }
    chunks = *(uint8_t ***)(global + 0x10);
    if (chunks == NULL) {
        return 0;
    }
    chunk = chunks[index / OBJECTS_PER_CHUNK];
    if (chunk == NULL) {
        return 0;
    }
    item = chunk + ((size_t)(index % OBJECTS_PER_CHUNK) * UOBJECT_ITEM_SIZE);
    return *(void **)item == object;
}

static int object_class_name(void *object, char *out, size_t out_capacity)
{
    uint8_t *class_object;

    if (object == NULL || out == NULL || out_capacity < 2) {
        return 0;
    }
    class_object = *(uint8_t **)((uint8_t *)object + UOBJECT_CLASS_PRIVATE);
    if (class_object == NULL) {
        return 0;
    }
    return fname_to_utf8(
        (const FName *)(class_object + UOBJECT_NAME_PRIVATE),
        out,
        out_capacity
    );
}

static int field_class_name(
    uint8_t *field,
    char *out,
    size_t out_capacity
)
{
    uint8_t *field_class;

    if (field == NULL || out == NULL || out_capacity < 2) {
        return 0;
    }
    field_class = *(uint8_t **)(field + FFIELD_CLASS_PRIVATE);
    if (field_class == NULL) {
        return 0;
    }
    return fname_to_utf8((const FName *)field_class, out, out_capacity);
}

static void *object_property_value(void *object, const char *property_name)
{
    uint8_t *property;
    int32_t offset;
    void *value;
    int32_t ignored_index;
    int32_t ignored_serial;

    property = find_property(object, property_name);
    if (property == NULL) {
        return NULL;
    }
    offset = *(int32_t *)(property + FPROPERTY_OFFSET_INTERNAL);
    if (offset < 0 || offset > 0x1000000) {
        return NULL;
    }
    value = *(void **)((uint8_t *)object + offset);
    if (!get_object_identity(value, &ignored_index, &ignored_serial)) {
        return NULL;
    }
    return value;
}

static int append_runtime_target(
    RuntimeTarget *targets,
    int target_capacity,
    int target_count,
    void *object,
    const char *kind,
    int player_slot
)
{
    RuntimeTarget *target;
    int32_t object_index;
    int32_t object_serial;

    if (targets == NULL || target_count < 0 ||
        target_count >= target_capacity || object == NULL || kind == NULL ||
        !get_object_identity(object, &object_index, &object_serial)) {
        return target_count;
    }
    target = &targets[target_count];
    memset(target, 0, sizeof(*target));
    target->object = object;
    target->player_slot = player_slot;
    snprintf(
        target->id,
        sizeof(target->id),
        "%s:%d:%d",
        kind,
        object_index,
        object_serial
    );
    snprintf(target->kind, sizeof(target->kind), "%s", kind);
    object_class_name(
        object,
        target->class_name,
        sizeof(target->class_name)
    );
    return target_count + 1;
}

static int copy_quoted_ascii_field(
    const char *text,
    const char *field_name,
    char *out,
    size_t out_capacity
)
{
    char marker[96];
    const char *value;
    size_t length = 0;

    if (text == NULL || field_name == NULL || out == NULL ||
        out_capacity < 2 ||
        snprintf(marker, sizeof(marker), "%s=\"", field_name) <= 0) {
        return 0;
    }
    value = strstr(text, marker);
    if (value == NULL) {
        return 0;
    }
    value += strlen(marker);
    while (*value != '\0' && *value != '"' && length + 1 < out_capacity) {
        unsigned char character = (unsigned char)*value++;

        if (!((character >= 'a' && character <= 'z') ||
              (character >= 'A' && character <= 'Z') ||
              (character >= '0' && character <= '9') ||
              character == '-' || character == '_' ||
              character == '.' || character == ':')) {
            out[0] = '\0';
            return 0;
        }
        out[length++] = (char)character;
    }
    if (*value != '"' || length == 0 || length + 1 >= out_capacity) {
        out[0] = '\0';
        return 0;
    }
    out[length] = '\0';
    return 1;
}

static int copy_unquoted_ascii_field(
    const char *text,
    const char *field_name,
    char *out,
    size_t out_capacity
)
{
    char marker[96];
    const char *value;
    size_t length = 0;

    if (text == NULL || field_name == NULL || out == NULL ||
        out_capacity < 2 ||
        snprintf(marker, sizeof(marker), "%s=", field_name) <= 0) {
        return 0;
    }
    value = strstr(text, marker);
    if (value == NULL) {
        return 0;
    }
    value += strlen(marker);
    while (*value != '\0' && *value != ',' && *value != ')' &&
           length + 1 < out_capacity) {
        unsigned char character = (unsigned char)*value++;

        if (!((character >= 'a' && character <= 'z') ||
              (character >= 'A' && character <= 'Z') ||
              (character >= '0' && character <= '9') ||
              character == '-' || character == '_' ||
              character == '.')) {
            out[0] = '\0';
            return 0;
        }
        out[length++] = (char)character;
    }
    if ((*value != ',' && *value != ')' && *value != '\0') ||
        length == 0 || length + 1 >= out_capacity) {
        out[0] = '\0';
        return 0;
    }
    out[length] = '\0';
    return 1;
}

static int steam_id64_is_valid(const char *value)
{
    size_t index;

    if (value == NULL || strlen(value) != 17) {
        return 0;
    }
    for (index = 0; index < 17; ++index) {
        if (value[index] < '0' || value[index] > '9') {
            return 0;
        }
    }
    return 1;
}

static int platform_account_id_is_valid(const char *value)
{
    size_t index;
    size_t length;

    if (value == NULL) {
        return 0;
    }
    length = strlen(value);
    if (length < 3 || length > 127) {
        return 0;
    }
    for (index = 0; index < length; ++index) {
        unsigned char character = (unsigned char)value[index];

        if (!((character >= 'a' && character <= 'z') ||
              (character >= 'A' && character <= 'Z') ||
              (character >= '0' && character <= '9') ||
              character == '-' || character == '_' ||
              character == '.' || character == ':')) {
            return 0;
        }
    }
    return 1;
}

static int exported_number(
    uint8_t *property,
    void *object,
    double *value
)
{
    char text[128];
    char *end;
    double parsed;

    if (property == NULL || object == NULL || value == NULL ||
        !export_property_utf8(
            property,
            object,
            0,
            text,
            sizeof(text))) {
        return 0;
    }
    parsed = strtod(text, &end);
    if (end == text) {
        return 0;
    }
    while (*end == ' ' || *end == '\t' ||
           *end == '\r' || *end == '\n') {
        ++end;
    }
    if (*end != '\0' || parsed < 0.0 || parsed > 60000.0) {
        return 0;
    }
    *value = parsed;
    return 1;
}

static void populate_runtime_player_identity(
    RuntimeTarget *controller_target,
    void *player_state
)
{
    uint8_t *property;
    char playfab_player[4096];
    char platform[32] = "";
    char platform_account_id[128] = "";
    double ping = 0.0;

    if (controller_target == NULL || player_state == NULL ||
        strcmp(controller_target->kind, "player_controller") != 0) {
        return;
    }
    property = find_property(player_state, "PlayerNamePrivate");
    if (property != NULL) {
        export_property_utf8(
            property,
            player_state,
            0,
            controller_target->player_name,
            sizeof(controller_target->player_name)
        );
    }
    property = find_property(player_state, "ExactPing");
    if (exported_number(property, player_state, &ping)) {
        controller_target->ping_ms = (int)(ping + 0.5);
        controller_target->has_ping = 1;
    } else {
        property = find_property(player_state, "Ping");
        if (exported_number(property, player_state, &ping) &&
            ping <= 15000.0) {
            controller_target->ping_ms = (int)(ping * 4.0 + 0.5);
            controller_target->has_ping = 1;
        }
    }
    property = find_property(player_state, "PlayFabPlayer");
    if (property != NULL &&
        export_property_utf8(
            property,
            player_state,
            0,
            playfab_player,
            sizeof(playfab_player))) {
        copy_quoted_ascii_field(
            playfab_player,
            "PlayFabId",
            controller_target->playfab_id,
            sizeof(controller_target->playfab_id)
        );
        if (copy_unquoted_ascii_field(
                playfab_player,
                "Platform",
                platform,
                sizeof(platform)) &&
            copy_quoted_ascii_field(
                playfab_player,
                "PlatformAccountID",
                platform_account_id,
                sizeof(platform_account_id)) &&
            ((strcmp(platform, "Steam") == 0 &&
              steam_id64_is_valid(platform_account_id)) ||
             ((strcmp(platform, "Epic") == 0 ||
               strcmp(platform, "EpicGames") == 0 ||
               strcmp(platform, "EOS") == 0) &&
              platform_account_id_is_valid(platform_account_id)))) {
            snprintf(
                controller_target->platform,
                sizeof(controller_target->platform),
                "%s",
                strcmp(platform, "Steam") == 0 ? "Steam" : "Epic"
            );
            snprintf(
                controller_target->platform_account_id,
                sizeof(controller_target->platform_account_id),
                "%s",
                platform_account_id
            );
        }
    }
}

static int collect_runtime_targets(
    void *world,
    RuntimeTarget *targets,
    int target_capacity,
    int *player_count
)
{
    WeakObjectArray *controllers;
    void *game_mode;
    void *game_state;
    int count = 0;
    int players = 0;
    int index;

    if (player_count != NULL) {
        *player_count = 0;
    }
    if (world == NULL || targets == NULL || target_capacity < 1) {
        return 0;
    }
    game_mode = *(void **)((uint8_t *)world + UWORLD_AUTHORITY_GAME_MODE);
    game_state = *(void **)((uint8_t *)world + UWORLD_GAME_STATE);
    count = append_runtime_target(
        targets,
        target_capacity,
        count,
        game_mode,
        "game_mode",
        -1
    );
    count = append_runtime_target(
        targets,
        target_capacity,
        count,
        game_state,
        "game_state",
        -1
    );

    controllers = (WeakObjectArray *)(
        (uint8_t *)world + UWORLD_PLAYER_CONTROLLER_LIST
    );
    if (controllers->count < 0 || controllers->count > 1024 ||
        controllers->capacity < controllers->count ||
        controllers->capacity > 4096 ||
        (controllers->count > 0 && controllers->data == NULL)) {
        return count;
    }
    for (index = 0;
         index < controllers->count && count < target_capacity;
         ++index) {
        void *controller = resolve_weak_object(&controllers->data[index]);
        void *player_state;
        void *pawn;
        int controller_target_index;

        if (controller == NULL) {
            continue;
        }
        ++players;
        controller_target_index = count;
        count = append_runtime_target(
            targets,
            target_capacity,
            count,
            controller,
            "player_controller",
            players - 1
        );
        player_state = object_property_value(controller, "PlayerState");
        count = append_runtime_target(
            targets,
            target_capacity,
            count,
            player_state,
            "player_state",
            players - 1
        );
        if (controller_target_index < count &&
            targets[controller_target_index].object == controller) {
            populate_runtime_player_identity(
                &targets[controller_target_index],
                player_state
            );
        }
        pawn = object_property_value(controller, "Pawn");
        if (pawn == NULL) {
            pawn = object_property_value(controller, "AcknowledgedPawn");
        }
        count = append_runtime_target(
            targets,
            target_capacity,
            count,
            pawn,
            "pawn",
            players - 1
        );
    }
    if (player_count != NULL) {
        *player_count = players;
    }
    return count;
}

static RuntimeTarget *find_runtime_target(
    RuntimeTarget *targets,
    int target_count,
    const char *target_id
)
{
    int index;

    if (targets == NULL || target_id == NULL) {
        return NULL;
    }
    for (index = 0; index < target_count; ++index) {
        if (strcmp(targets[index].id, target_id) == 0) {
            return &targets[index];
        }
    }
    return NULL;
}

static const char *replication_condition_name(uint8_t condition)
{
    switch (condition) {
    case 0:
        return "None";
    case 1:
        return "InitialOnly";
    case 2:
        return "OwnerOnly";
    case 3:
        return "SkipOwner";
    case 4:
        return "SimulatedOnly";
    case 5:
        return "AutonomousOnly";
    case 6:
        return "SimulatedOrPhysics";
    case 7:
        return "InitialOrOwner";
    case 8:
        return "Custom";
    case 9:
        return "ReplayOrOwner";
    case 10:
        return "ReplayOnly";
    case 11:
        return "SimulatedOnlyNoReplay";
    case 12:
        return "SimulatedOrPhysicsNoReplay";
    case 13:
        return "SkipReplay";
    case 15:
        return "Never";
    default:
        return "Unknown";
    }
}

static const char *property_replication_scope(
    const RuntimeTarget *target,
    uint64_t flags,
    uint8_t condition
)
{
    if (target == NULL || strcmp(target->kind, "game_mode") == 0 ||
        (flags & CPF_NET) == 0 || (flags & CPF_REP_SKIP) != 0 ||
        condition == 15) {
        return "server_only";
    }
    switch (condition) {
    case 0:
        return "replicated";
    case 1:
        return "initial_only";
    case 2:
        return "owner_only";
    case 3:
        return "skip_owner";
    case 4:
    case 11:
        return "simulated_only";
    case 5:
        return "autonomous_only";
    case 6:
    case 12:
        return "simulated_or_physics";
    case 7:
        return "initial_or_owner";
    case 8:
        return "custom";
    case 9:
        return "replay_or_owner";
    case 10:
        return "replay_only";
    case 13:
        return "skip_replay";
    default:
        return "conditional";
    }
}

static int property_type_is_editable(
    const char *type_name,
    uint64_t flags,
    const char **read_only_reason
)
{
    static const char *const editable_types[] = {
        "ByteProperty",
        "Int8Property",
        "Int16Property",
        "IntProperty",
        "Int64Property",
        "UInt16Property",
        "UInt32Property",
        "UInt64Property",
        "FloatProperty",
        "DoubleProperty",
        "BoolProperty",
        "EnumProperty",
        "NameProperty",
        "StrProperty",
        "TextProperty",
        "StructProperty",
        "ArrayProperty",
        "SetProperty",
        "MapProperty",
    };
    size_t index;

    if (read_only_reason != NULL) {
        *read_only_reason = "";
    }
    if ((flags & (CPF_PARM | CPF_OUT_PARM | CPF_RETURN_PARM)) != 0) {
        if (read_only_reason != NULL) {
            *read_only_reason = "function_parameter";
        }
        return 0;
    }
    if ((flags & CPF_DEPRECATED) != 0) {
        if (read_only_reason != NULL) {
            *read_only_reason = "deprecated";
        }
        return 0;
    }
    if ((flags & CPF_EDITOR_ONLY) != 0) {
        if (read_only_reason != NULL) {
            *read_only_reason = "editor_only";
        }
        return 0;
    }
    for (index = 0; index < ARRAY_COUNT(editable_types); ++index) {
        if (strcmp(type_name, editable_types[index]) == 0) {
            return 1;
        }
    }
    if (read_only_reason != NULL) {
        *read_only_reason = "unsafe_reference_or_delegate_type";
    }
    return 0;
}

static uint8_t *find_declared_property(
    void *object,
    const char *declaring_class_name,
    const char *property_name
)
{
    uint8_t *class_object;
    int class_depth = 0;
    char class_name[256];
    char field_name[256];

    if (object == NULL || declaring_class_name == NULL ||
        property_name == NULL) {
        return NULL;
    }
    class_object = *(uint8_t **)((uint8_t *)object + UOBJECT_CLASS_PRIVATE);
    while (class_object != NULL && class_depth++ < 128) {
        uint8_t *field;
        int field_count = 0;

        if (fname_to_utf8(
                (const FName *)(class_object + UOBJECT_NAME_PRIVATE),
                class_name,
                sizeof(class_name)) <= 0) {
            return NULL;
        }
        if (strcmp(class_name, declaring_class_name) != 0) {
            class_object = *(uint8_t **)(
                class_object + USTRUCT_SUPER_STRUCT
            );
            continue;
        }
        field = *(uint8_t **)(class_object + USTRUCT_CHILD_PROPERTIES);
        while (field != NULL && field_count++ < MAX_PROPERTIES_PER_TARGET) {
            if (fname_to_utf8(
                    (const FName *)(field + FFIELD_NAME_PRIVATE),
                    field_name,
                    sizeof(field_name)) > 0 &&
                strcmp(field_name, property_name) == 0) {
                return field;
            }
            field = *(uint8_t **)(field + FFIELD_NEXT);
        }
        return NULL;
    }
    return NULL;
}

static void *property_value_pointer(
    uint8_t *property,
    void *object,
    int array_index
)
{
    uint8_t *class_object;
    int32_t properties_size;
    int32_t array_dim;
    int32_t element_size;
    int32_t offset;
    uint64_t end_offset;

    if (property == NULL || object == NULL) {
        return NULL;
    }
    class_object = *(uint8_t **)((uint8_t *)object + UOBJECT_CLASS_PRIVATE);
    if (class_object == NULL) {
        return NULL;
    }
    properties_size = *(int32_t *)(class_object + USTRUCT_PROPERTIES_SIZE);
    array_dim = *(int32_t *)(property + FPROPERTY_ARRAY_DIM);
    element_size = *(int32_t *)(property + FPROPERTY_ELEMENT_SIZE);
    offset = *(int32_t *)(property + FPROPERTY_OFFSET_INTERNAL);
    if (properties_size < 1 || properties_size > 0x1000000 ||
        array_dim < 1 || array_dim > 1024 ||
        element_size < 1 || element_size > 0x1000000 ||
        array_index < 0 || array_index >= array_dim || offset < 0) {
        return NULL;
    }
    end_offset = (uint64_t)(uint32_t)offset +
        ((uint64_t)(uint32_t)element_size * (uint32_t)(array_index + 1));
    if (end_offset > (uint64_t)(uint32_t)properties_size) {
        return NULL;
    }
    return (uint8_t *)object + offset +
        ((size_t)element_size * (size_t)array_index);
}

static wchar_t *utf8_to_wide(const char *value)
{
    wchar_t *result;
    int wide_length;

    if (value == NULL) {
        return NULL;
    }
    wide_length = MultiByteToWideChar(
        CP_UTF8,
        MB_ERR_INVALID_CHARS,
        value,
        -1,
        NULL,
        0
    );
    if (wide_length < 1 ||
        (size_t)wide_length > (MAX_TEXT_VALUE_BYTES * 2U)) {
        return NULL;
    }
    result = (wchar_t *)HeapAlloc(
        GetProcessHeap(),
        0,
        (size_t)wide_length * sizeof(wchar_t)
    );
    if (result == NULL) {
        return NULL;
    }
    if (MultiByteToWideChar(
            CP_UTF8,
            MB_ERR_INVALID_CHARS,
            value,
            -1,
            result,
            wide_length) != wide_length) {
        HeapFree(GetProcessHeap(), 0, result);
        return NULL;
    }
    return result;
}

static int import_property_utf8(
    uint8_t *property,
    void *object,
    int array_index,
    const char *new_value
)
{
    wchar_t *wide_value;
    const wchar_t *end;
    void *value_pointer;
    void *error_output;
    int success;

    if (g_property_import_text == NULL || new_value == NULL) {
        return 0;
    }
    value_pointer = property_value_pointer(property, object, array_index);
    if (value_pointer == NULL) {
        return 0;
    }
    wide_value = utf8_to_wide(new_value);
    if (wide_value == NULL) {
        return 0;
    }
    error_output = *(void **)(g_image_base + RVA_GWARN);
    end = g_property_import_text(
        property,
        wide_value,
        value_pointer,
        0,
        object,
        error_output
    );
    if (end != NULL) {
        while (*end == L' ' || *end == L'\t' ||
               *end == L'\r' || *end == L'\n') {
            ++end;
        }
    }
    success = end != NULL && *end == L'\0';
    HeapFree(GetProcessHeap(), 0, wide_value);
    return success;
}

static int runtime_account_progress(
    const RuntimeTarget *target,
    int *xp,
    int *level
)
{
    wchar_t playfab_wide[128];
    FString playfab_id;
    void *inventory;
    size_t length;
    size_t index;
    int player_xp;
    int player_level;

    if (target == NULL || xp == NULL || level == NULL ||
        strcmp(target->kind, "player_controller") != 0 ||
        target->playfab_id[0] == '\0' ||
        g_inventory_get_player_xp == NULL ||
        g_inventory_is_available == NULL ||
        g_utility_get_inventory == NULL ||
        g_utility_get_rank_from_xp == NULL) {
        return 0;
    }
    length = strlen(target->playfab_id);
    if (length == 0 || length >= ARRAY_COUNT(playfab_wide)) {
        return 0;
    }
    for (index = 0; index < length; ++index) {
        unsigned char character = (unsigned char)target->playfab_id[index];

        if (!((character >= 'a' && character <= 'z') ||
              (character >= 'A' && character <= 'Z') ||
              (character >= '0' && character <= '9'))) {
            return 0;
        }
        playfab_wide[index] = (wchar_t)character;
    }
    playfab_wide[length] = L'\0';
    playfab_id.data = playfab_wide;
    playfab_id.count = (int32_t)length + 1;
    playfab_id.capacity = (int32_t)length + 1;

    inventory = g_utility_get_inventory();
    if (!is_registered_uobject(inventory) ||
        !g_inventory_is_available(inventory, &playfab_id)) {
        return 0;
    }
    player_xp = g_inventory_get_player_xp(inventory, &playfab_id);
    if (player_xp < 0) {
        return 0;
    }
    player_level = g_utility_get_rank_from_xp(player_xp);
    if (player_level < 1) {
        return 0;
    }
    *xp = player_xp;
    *level = player_level;
    return 1;
}

static void append_target_json(
    JsonBuilder *builder,
    const RuntimeTarget *target
)
{
    json_append(builder, "{\"id\":");
    json_append_string(builder, target->id);
    json_append(builder, ",\"kind\":");
    json_append_string(builder, target->kind);
    json_append(builder, ",\"class\":");
    json_append_string(builder, target->class_name);
    json_append_format(
        builder,
        ",\"player_slot\":%d",
        target->player_slot
    );
    if (target->player_name[0] != '\0') {
        json_append(builder, ",\"player_name\":");
        json_append_string(builder, target->player_name);
    }
    if (target->playfab_id[0] != '\0') {
        json_append(builder, ",\"playfab_id\":");
        json_append_string(builder, target->playfab_id);
    }
    if (target->platform[0] != '\0') {
        json_append(builder, ",\"platform\":");
        json_append_string(builder, target->platform);
    }
    if (target->platform_account_id[0] != '\0') {
        json_append(builder, ",\"platform_account_id\":");
        json_append_string(builder, target->platform_account_id);
    }
    if (target->has_ping) {
        json_append_format(builder, ",\"ping_ms\":%d", target->ping_ms);
    }
    json_append(builder, "}");
}

static void build_error_response(
    JsonBuilder *builder,
    const char *request_id,
    const char *code,
    const char *message
)
{
    json_append(builder, "{\"version\":1,\"request_id\":");
    json_append_string(builder, request_id == NULL ? "invalid" : request_id);
    json_append(builder, ",\"ok\":false,\"error\":{\"code\":");
    json_append_string(builder, code == NULL ? "internal_error" : code);
    json_append(builder, ",\"message\":");
    json_append_string(builder, message == NULL ? "Bridge error." : message);
    json_append(builder, "}}\n");
}

static void append_replication_json(
    JsonBuilder *builder,
    const RuntimeTarget *target,
    uint64_t flags,
    uint16_t rep_index,
    uint8_t condition
)
{
    json_append(builder, "\"replication\":{\"net\":");
    json_append(builder, (flags & CPF_NET) != 0 ? "true" : "false");
    json_append(builder, ",\"rep_skip\":");
    json_append(builder, (flags & CPF_REP_SKIP) != 0 ? "true" : "false");
    json_append(builder, ",\"rep_notify\":");
    json_append(builder, (flags & CPF_REP_NOTIFY) != 0 ? "true" : "false");
    json_append(builder, ",\"scope\":");
    json_append_string(
        builder,
        property_replication_scope(target, flags, condition)
    );
    json_append(builder, ",\"condition\":");
    json_append_string(builder, replication_condition_name(condition));
    json_append_format(builder, ",\"rep_index\":%u}", (unsigned)rep_index);
}

static void *property_enum_object(
    uint8_t *property,
    const char *type_name
)
{
    void *enum_object = NULL;
    char class_name[64] = "";

    if (property == NULL || type_name == NULL) {
        return NULL;
    }
    if (strcmp(type_name, "ByteProperty") == 0) {
        enum_object = *(void **)(property + FBYTEPROPERTY_ENUM);
    } else if (strcmp(type_name, "EnumProperty") == 0) {
        enum_object = *(void **)(property + FENUMPROPERTY_ENUM);
    }
    if (!is_registered_uobject(enum_object) ||
        object_class_name(enum_object, class_name, sizeof(class_name)) <= 0 ||
        (strcmp(class_name, "Enum") != 0 &&
         strcmp(class_name, "UserDefinedEnum") != 0)) {
        return NULL;
    }
    return enum_object;
}

static const char *short_enum_value_name(char *name)
{
    char *cursor;
    char *short_name;

    if (name == NULL) {
        return "";
    }
    short_name = name;
    cursor = name;
    while ((cursor = strstr(cursor, "::")) != NULL) {
        short_name = cursor + 2;
        cursor += 2;
    }
    return short_name;
}

static int enum_value_is_sentinel(const char *name)
{
    size_t length;

    if (name == NULL) {
        return 1;
    }
    length = strlen(name);
    return strcmp(name, "MAX") == 0 ||
        (length >= 4 && strcmp(name + length - 4, "_MAX") == 0);
}

static void append_enum_values_json(
    JsonBuilder *builder,
    uint8_t *property,
    const char *type_name
)
{
    uint8_t *enum_object;
    uint8_t *names;
    int32_t count;
    int32_t capacity;
    int index;
    int emitted = 0;

    json_append(builder, "\"enum_values\":[");
    enum_object = (uint8_t *)property_enum_object(property, type_name);
    if (enum_object != NULL) {
        names = *(uint8_t **)(enum_object + UENUM_NAMES_DATA);
        count = *(int32_t *)(enum_object + UENUM_NAMES_COUNT);
        capacity = *(int32_t *)(enum_object + UENUM_NAMES_CAPACITY);
        if (names != NULL && count > 0 && count <= MAX_ENUM_VALUES &&
            capacity >= count && capacity <= MAX_ENUM_VALUES * 4) {
            for (index = 0; index < count; ++index) {
                char enum_name[256] = "";
                const char *short_name;

                if (fname_to_utf8(
                        (const FName *)(
                            names + ((size_t)index * UENUM_NAME_VALUE_SIZE)
                        ),
                        enum_name,
                        sizeof(enum_name)) <= 0) {
                    continue;
                }
                short_name = short_enum_value_name(enum_name);
                if (short_name[0] == '\0' ||
                    enum_value_is_sentinel(short_name)) {
                    continue;
                }
                if (emitted++ > 0) {
                    json_append(builder, ",");
                }
                json_append_string(builder, short_name);
            }
        }
    }
    json_append(builder, "]");
}

static int append_property_json(
    JsonBuilder *builder,
    const RuntimeTarget *target,
    uint8_t *property,
    const char *declaring_class,
    const char *property_name,
    int array_index
)
{
    char type_name[128] = "";
    char rep_notify_name[256] = "";
    const char *read_only_reason = "";
    uint64_t flags;
    uint16_t rep_index;
    uint8_t condition;
    int32_t array_dim;
    int32_t element_size;
    int32_t offset;
    int editable;
    int value_available;

    if (builder == NULL || target == NULL || property == NULL ||
        declaring_class == NULL || property_name == NULL ||
        property_value_pointer(property, target->object, array_index) == NULL ||
        field_class_name(property, type_name, sizeof(type_name)) <= 0) {
        return 0;
    }
    flags = *(uint64_t *)(property + FPROPERTY_FLAGS);
    rep_index = *(uint16_t *)(property + FPROPERTY_REP_INDEX);
    condition = *(uint8_t *)(property + FPROPERTY_REP_CONDITION);
    array_dim = *(int32_t *)(property + FPROPERTY_ARRAY_DIM);
    element_size = *(int32_t *)(property + FPROPERTY_ELEMENT_SIZE);
    offset = *(int32_t *)(property + FPROPERTY_OFFSET_INTERNAL);
    editable = property_type_is_editable(
        type_name,
        flags,
        &read_only_reason
    );
    value_available = export_property_utf8(
        property,
        target->object,
        array_index,
        g_value_buffer,
        sizeof(g_value_buffer)
    );
    if (strcmp(property_name, "UberGraphFrame") == 0) {
        editable = 0;
        read_only_reason = "engine_internal";
    } else if (!value_available) {
        editable = 0;
        read_only_reason = "value_unavailable";
    }
    if ((flags & CPF_REP_NOTIFY) != 0) {
        fname_to_utf8(
            (const FName *)(property + FPROPERTY_REP_NOTIFY_FUNC),
            rep_notify_name,
            sizeof(rep_notify_name)
        );
    }

    json_append(builder, "{\"declaring_class\":");
    json_append_string(builder, declaring_class);
    json_append(builder, ",\"name\":");
    json_append_string(builder, property_name);
    json_append(builder, ",\"type\":");
    json_append_string(builder, type_name);
    json_append_format(
        builder,
        ",\"array_index\":%d,\"array_dim\":%d,"
        "\"element_size\":%d,\"offset\":%d,"
        "\"flags\":\"0x%016llx\",\"editable\":%s,"
        "\"read_only_reason\":",
        array_index,
        array_dim,
        element_size,
        offset,
        (unsigned long long)flags,
        editable ? "true" : "false"
    );
    json_append_string(builder, read_only_reason);
    json_append(builder, ",\"rep_notify_function\":");
    json_append_string(builder, rep_notify_name);
    json_append(builder, ",\"value\":");
    if (value_available) {
        json_append_string(builder, g_value_buffer);
    } else {
        json_append(builder, "null");
    }
    if (strcmp(type_name, "ByteProperty") == 0 ||
        strcmp(type_name, "EnumProperty") == 0) {
        json_append(builder, ",");
        append_enum_values_json(builder, property, type_name);
    }
    json_append(builder, ",");
    append_replication_json(
        builder,
        target,
        flags,
        rep_index,
        condition
    );
    json_append(builder, "}");
    return 1;
}

static void build_status_response(
    JsonBuilder *builder,
    const BridgeRequest *request,
    void *world
)
{
    int player_count = 0;
    int target_count;
    int index;

    target_count = collect_runtime_targets(
        world,
        g_targets,
        MAX_RUNTIME_TARGETS,
        &player_count
    );
    json_append(builder, "{\"version\":1,\"request_id\":");
    json_append_string(builder, request->request_id);
    json_append(
        builder,
        ",\"ok\":true,\"ready\":true,\"player_controller_count\":"
    );
    json_append_format(
        builder,
        "%d,\"target_count\":%d,\"targets\":[",
        player_count,
        target_count
    );
    for (index = 0; index < target_count; ++index) {
        if (index > 0) {
            json_append(builder, ",");
        }
        append_target_json(builder, &g_targets[index]);
    }
    json_append(builder, "]}\n");
}

static void build_get_response(
    JsonBuilder *builder,
    const BridgeRequest *request,
    void *world
)
{
    RuntimeTarget *target;
    uint8_t *class_object;
    int player_count = 0;
    int target_count;
    int class_depth;
    int property_count = 0;
    int first;
    int account_xp;
    int account_level;

    target_count = collect_runtime_targets(
        world,
        g_targets,
        MAX_RUNTIME_TARGETS,
        &player_count
    );
    target = find_runtime_target(
        g_targets,
        target_count,
        request->target_id
    );
    if (target == NULL) {
        build_error_response(
            builder,
            request->request_id,
            "target_not_found",
            "The runtime target no longer exists."
        );
        return;
    }

    json_append(builder, "{\"version\":1,\"request_id\":");
    json_append_string(builder, request->request_id);
    json_append(
        builder,
        ",\"ok\":true,\"player_controller_count\":"
    );
    json_append_format(builder, "%d,\"target\":", player_count);
    append_target_json(builder, target);
    if (runtime_account_progress(target, &account_xp, &account_level)) {
        json_append_format(
            builder,
            ",\"account_progress\":{\"xp\":%d,\"level\":%d}",
            account_xp,
            account_level
        );
    }
    json_append(builder, ",\"class_chain\":[");
    class_object = *(uint8_t **)((uint8_t *)target->object + UOBJECT_CLASS_PRIVATE);
    class_depth = 0;
    first = 1;
    while (class_object != NULL && class_depth++ < 128) {
        char class_name[256];

        if (fname_to_utf8(
                (const FName *)(class_object + UOBJECT_NAME_PRIVATE),
                class_name,
                sizeof(class_name)) > 0) {
            if (!first) {
                json_append(builder, ",");
            }
            json_append_string(builder, class_name);
            first = 0;
        }
        class_object = *(uint8_t **)(class_object + USTRUCT_SUPER_STRUCT);
    }
    json_append(builder, "],\"properties\":[");

    class_object = *(uint8_t **)((uint8_t *)target->object + UOBJECT_CLASS_PRIVATE);
    class_depth = 0;
    first = 1;
    while (class_object != NULL && class_depth++ < 128 &&
           property_count < MAX_PROPERTIES_PER_TARGET) {
        uint8_t *field;
        int field_count = 0;
        char declaring_class[256];

        if (fname_to_utf8(
                (const FName *)(class_object + UOBJECT_NAME_PRIVATE),
                declaring_class,
                sizeof(declaring_class)) <= 0) {
            break;
        }
        field = *(uint8_t **)(class_object + USTRUCT_CHILD_PROPERTIES);
        while (field != NULL &&
               field_count++ < MAX_PROPERTIES_PER_TARGET &&
               property_count < MAX_PROPERTIES_PER_TARGET) {
            char property_name[256];
            int32_t array_dim;
            int array_index;

            if (fname_to_utf8(
                    (const FName *)(field + FFIELD_NAME_PRIVATE),
                    property_name,
                    sizeof(property_name)) <= 0) {
                field = *(uint8_t **)(field + FFIELD_NEXT);
                continue;
            }
            array_dim = *(int32_t *)(field + FPROPERTY_ARRAY_DIM);
            if (array_dim < 1 || array_dim > 1024) {
                field = *(uint8_t **)(field + FFIELD_NEXT);
                continue;
            }
            for (array_index = 0;
                 array_index < array_dim &&
                 property_count < MAX_PROPERTIES_PER_TARGET;
                 ++array_index) {
                size_t before = builder->length;

                if (!first) {
                    json_append(builder, ",");
                }
                if (append_property_json(
                        builder,
                        target,
                        field,
                        declaring_class,
                        property_name,
                        array_index)) {
                    first = 0;
                    ++property_count;
                } else {
                    builder->length = before;
                    if (!builder->failed) {
                        builder->data[before] = '\0';
                    }
                }
                if (builder->failed) {
                    return;
                }
            }
            field = *(uint8_t **)(field + FFIELD_NEXT);
        }
        class_object = *(uint8_t **)(class_object + USTRUCT_SUPER_STRUCT);
    }
    json_append_format(
        builder,
        "],\"property_count\":%d,"
        "\"network_note\":\"Only properties marked Net are eligible for "
        "Unreal replication; GameMode and non-Net properties remain "
        "server-only.\"}\n",
        property_count
    );
}

static void build_set_response(
    JsonBuilder *builder,
    const BridgeRequest *request,
    void *world
)
{
    RuntimeTarget *target;
    uint8_t *property;
    char type_name[128] = "";
    const char *read_only_reason = "";
    uint64_t flags;
    uint16_t rep_index;
    uint8_t condition;
    int target_count;
    int player_count;

    target_count = collect_runtime_targets(
        world,
        g_targets,
        MAX_RUNTIME_TARGETS,
        &player_count
    );
    target = find_runtime_target(
        g_targets,
        target_count,
        request->target_id
    );
    if (target == NULL) {
        build_error_response(
            builder,
            request->request_id,
            "target_not_found",
            "The runtime target no longer exists."
        );
        return;
    }
    property = find_declared_property(
        target->object,
        request->declaring_class,
        request->property_name
    );
    if (property == NULL ||
        property_value_pointer(
            property,
            target->object,
            request->array_index) == NULL) {
        build_error_response(
            builder,
            request->request_id,
            "property_not_found",
            "The runtime property no longer exists."
        );
        return;
    }
    if (field_class_name(property, type_name, sizeof(type_name)) <= 0) {
        build_error_response(
            builder,
            request->request_id,
            "property_type_unavailable",
            "The runtime property type is unavailable."
        );
        return;
    }
    flags = *(uint64_t *)(property + FPROPERTY_FLAGS);
    if (strcmp(request->property_name, "UberGraphFrame") == 0) {
        build_error_response(
            builder,
            request->request_id,
            "engine_internal",
            "Engine-internal frame storage is read-only."
        );
        return;
    }
    if (!property_type_is_editable(
            type_name,
            flags,
            &read_only_reason)) {
        build_error_response(
            builder,
            request->request_id,
            read_only_reason,
            "This property type is read-only in the runtime bridge."
        );
        return;
    }
    if (!export_property_utf8(
            property,
            target->object,
            request->array_index,
            g_value_buffer,
            sizeof(g_value_buffer))) {
        build_error_response(
            builder,
            request->request_id,
            "value_unavailable",
            "The current property value could not be exported."
        );
        return;
    }
    if (strcmp(g_value_buffer, request->expected_value) != 0) {
        json_append(builder, "{\"version\":1,\"request_id\":");
        json_append_string(builder, request->request_id);
        json_append(
            builder,
            ",\"ok\":false,\"error\":{\"code\":\"stale_value\","
            "\"message\":\"The property changed after it was loaded.\","
            "\"current_value\":"
        );
        json_append_string(builder, g_value_buffer);
        json_append(builder, "}}\n");
        return;
    }
    snprintf(
        g_secondary_value_buffer,
        sizeof(g_secondary_value_buffer),
        "%s",
        g_value_buffer
    );
    if (!import_property_utf8(
            property,
            target->object,
            request->array_index,
            request->new_value) ||
        !export_property_utf8(
            property,
            target->object,
            request->array_index,
            g_value_buffer,
            sizeof(g_value_buffer))) {
        import_property_utf8(
            property,
            target->object,
            request->array_index,
            g_secondary_value_buffer
        );
        build_error_response(
            builder,
            request->request_id,
            "invalid_value",
            "Unreal rejected the new property value."
        );
        return;
    }
    rep_index = *(uint16_t *)(property + FPROPERTY_REP_INDEX);
    condition = *(uint8_t *)(property + FPROPERTY_REP_CONDITION);
    if (strcmp(target->kind, "game_mode") != 0 &&
        (flags & CPF_NET) != 0 &&
        (flags & CPF_REP_SKIP) == 0 &&
        condition != 15 &&
        g_flush_net_dormancy != NULL) {
        g_flush_net_dormancy(target->object);
    }
    if (g_force_net_update != NULL) {
        g_force_net_update(target->object);
    }

    json_append(builder, "{\"version\":1,\"request_id\":");
    json_append_string(builder, request->request_id);
    json_append(builder, ",\"ok\":true,\"target\":");
    append_target_json(builder, target);
    json_append(builder, ",\"property\":{\"declaring_class\":");
    json_append_string(builder, request->declaring_class);
    json_append(builder, ",\"name\":");
    json_append_string(builder, request->property_name);
    json_append_format(
        builder,
        ",\"array_index\":%d,\"old_value\":",
        request->array_index
    );
    json_append_string(builder, g_secondary_value_buffer);
    json_append(builder, ",\"new_value\":");
    json_append_string(builder, g_value_buffer);
    json_append(builder, ",");
    append_replication_json(
        builder,
        target,
        flags,
        rep_index,
        condition
    );
    json_append(builder, "}}\n");
}

static void process_bridge_request(void *world)
{
    JsonBuilder builder;

    if (InterlockedCompareExchange(&g_request_state, 2, 1) != 1) {
        return;
    }
    json_init(&builder, g_response_buffer, sizeof(g_response_buffer));
    switch (g_request.command) {
    case BRIDGE_COMMAND_STATUS:
        build_status_response(&builder, &g_request, world);
        break;
    case BRIDGE_COMMAND_GET:
        build_get_response(&builder, &g_request, world);
        break;
    case BRIDGE_COMMAND_SET:
        build_set_response(&builder, &g_request, world);
        break;
    default:
        build_error_response(
            &builder,
            g_request.request_id,
            "invalid_command",
            "Unsupported bridge command."
        );
        break;
    }
    if (builder.failed) {
        json_init(&builder, g_response_buffer, sizeof(g_response_buffer));
        build_error_response(
            &builder,
            g_request.request_id,
            "response_too_large",
            "The runtime response exceeded the bridge size limit."
        );
    }
    g_response_length = builder.length;
    InterlockedExchange(&g_request_state, 3);
    SetEvent(g_response_event);
}

static int split_request_fields(
    char *value,
    char **fields,
    int field_capacity
)
{
    char *cursor;
    int count = 0;

    if (value == NULL || fields == NULL || field_capacity < 1) {
        return 0;
    }
    cursor = value;
    while (count < field_capacity) {
        char *separator;

        fields[count++] = cursor;
        separator = strchr(cursor, '\t');
        if (separator == NULL) {
            return count;
        }
        *separator = '\0';
        cursor = separator + 1;
    }
    return 0;
}

static int decoded_text_is_valid(
    const char *encoded,
    const char *decoded
)
{
    return encoded != NULL && decoded != NULL &&
        strlen(decoded) == strlen(encoded) / 2;
}

static int parse_bridge_request(char *data, BridgeRequest *request)
{
    char *fields[12];
    char *end;
    long array_index;
    size_t length;
    int field_count;

    if (data == NULL || request == NULL) {
        return 0;
    }
    length = strlen(data);
    while (length > 0 &&
           (data[length - 1] == '\r' || data[length - 1] == '\n')) {
        data[--length] = '\0';
    }
    if (strchr(data, '\r') != NULL || strchr(data, '\n') != NULL) {
        return 0;
    }
    field_count = split_request_fields(
        data,
        fields,
        (int)ARRAY_COUNT(fields)
    );
    if (field_count < 3 || strcmp(fields[0], "V1") != 0 ||
        !request_id_is_valid(fields[1])) {
        return 0;
    }

    memset(request, 0, sizeof(*request));
    snprintf(
        request->request_id,
        sizeof(request->request_id),
        "%s",
        fields[1]
    );
    if (strcmp(fields[2], "STATUS") == 0 && field_count == 3) {
        request->command = BRIDGE_COMMAND_STATUS;
        return 1;
    }
    if (strcmp(fields[2], "GET") == 0 && field_count == 4 &&
        fields[3][0] != '\0' &&
        strlen(fields[3]) < sizeof(request->target_id)) {
        request->command = BRIDGE_COMMAND_GET;
        snprintf(
            request->target_id,
            sizeof(request->target_id),
            "%s",
            fields[3]
        );
        return 1;
    }
    if (strcmp(fields[2], "SET") != 0 || field_count != 9 ||
        fields[3][0] == '\0' ||
        strlen(fields[3]) >= sizeof(request->target_id) ||
        !decode_hex(
            fields[4],
            request->declaring_class,
            sizeof(request->declaring_class)) ||
        !decoded_text_is_valid(fields[4], request->declaring_class) ||
        !decode_hex(
            fields[5],
            request->property_name,
            sizeof(request->property_name)) ||
        !decoded_text_is_valid(fields[5], request->property_name) ||
        !decode_hex(
            fields[7],
            request->expected_value,
            sizeof(request->expected_value)) ||
        !decoded_text_is_valid(fields[7], request->expected_value) ||
        !decode_hex(
            fields[8],
            request->new_value,
            sizeof(request->new_value)) ||
        !decoded_text_is_valid(fields[8], request->new_value)) {
        return 0;
    }
    array_index = strtol(fields[6], &end, 10);
    if (fields[6][0] == '\0' || *end != '\0' ||
        array_index < 0 || array_index > 1023) {
        return 0;
    }
    request->command = BRIDGE_COMMAND_SET;
    request->array_index = (int)array_index;
    snprintf(
        request->target_id,
        sizeof(request->target_id),
        "%s",
        fields[3]
    );
    return request->declaring_class[0] != '\0' &&
        request->property_name[0] != '\0';
}

static int read_request_file(
    char *buffer,
    size_t buffer_capacity,
    size_t *request_length
)
{
    HANDLE file;
    LARGE_INTEGER size;
    DWORD read_length;

    if (buffer == NULL || buffer_capacity < 2 || request_length == NULL) {
        return 0;
    }
    file = CreateFileW(
        g_request_path,
        GENERIC_READ,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
        NULL,
        OPEN_EXISTING,
        FILE_ATTRIBUTE_NORMAL,
        NULL
    );
    if (file == INVALID_HANDLE_VALUE) {
        return 0;
    }
    if (!GetFileSizeEx(file, &size) || size.QuadPart < 1 ||
        (uint64_t)size.QuadPart >= buffer_capacity ||
        size.QuadPart > MAX_REQUEST_BYTES ||
        !ReadFile(
            file,
            buffer,
            (DWORD)size.QuadPart,
            &read_length,
            NULL) ||
        read_length != (DWORD)size.QuadPart) {
        CloseHandle(file);
        DeleteFileW(g_request_path);
        return -1;
    }
    CloseHandle(file);
    DeleteFileW(g_request_path);
    buffer[read_length] = '\0';
    if (strlen(buffer) != read_length) {
        return -1;
    }
    *request_length = read_length;
    return 1;
}

static void write_worker_error(
    const char *request_id,
    const char *code,
    const char *message
)
{
    JsonBuilder builder;
    char response[1024];

    json_init(&builder, response, sizeof(response));
    build_error_response(&builder, request_id, code, message);
    if (!builder.failed) {
        write_atomic_file(
            g_response_temp_path,
            g_response_path,
            response,
            builder.length
        );
    }
}

static DWORD WINAPI bridge_worker_thread(LPVOID parameter)
{
    char *request_buffer;

    (void)parameter;
    request_buffer = (char *)HeapAlloc(
        GetProcessHeap(),
        0,
        MAX_REQUEST_BYTES + 1U
    );
    if (request_buffer == NULL) {
        append_log("runtime bridge IPC disabled: request allocation failed");
        return 0;
    }

    for (;;) {
        BridgeRequest parsed_request;
        size_t request_length = 0;
        int read_result;

        if (InterlockedCompareExchange(&g_status_state, 2, 1) == 1) {
            if (!write_atomic_file(
                    g_status_temp_path,
                    g_status_path,
                    g_status_buffer,
                    g_status_length)) {
                append_log("runtime bridge status write failed");
            }
            InterlockedExchange(&g_status_state, 0);
        }
        if (InterlockedCompareExchange(&g_request_state, 0, 0) != 0) {
            Sleep(10);
            continue;
        }
        read_result = read_request_file(
            request_buffer,
            MAX_REQUEST_BYTES + 1U,
            &request_length
        );
        if (read_result == 0) {
            Sleep(50);
            continue;
        }
        if (read_result < 0 ||
            request_length < 4 ||
            !parse_bridge_request(request_buffer, &parsed_request)) {
            write_worker_error(
                "invalid",
                "invalid_request",
                "Malformed runtime bridge request."
            );
            continue;
        }

        memcpy(&g_request, &parsed_request, sizeof(g_request));
        g_response_length = 0;
        ResetEvent(g_response_event);
        InterlockedExchange(&g_request_state, 1);
        for (;;) {
            DWORD wait_result = WaitForSingleObject(g_response_event, 1000);
            if (wait_result == WAIT_OBJECT_0 &&
                InterlockedCompareExchange(&g_request_state, 3, 3) == 3) {
                break;
            }
            if (wait_result == WAIT_FAILED) {
                append_log("runtime bridge IPC worker wait failed");
                InterlockedExchange(&g_request_state, 0);
                break;
            }
        }
        if (InterlockedCompareExchange(&g_request_state, 3, 3) == 3) {
            if (!write_atomic_file(
                    g_response_temp_path,
                    g_response_path,
                    g_response_buffer,
                    g_response_length)) {
                append_log("runtime bridge IPC response write failed");
            }
            InterlockedExchange(&g_request_state, 0);
        }
    }
}

static void write_status(void *world, void *game_mode)
{
    JsonBuilder builder;
    char game_mode_class[160] = "";
    char num_players[64] = "";
    void *game_mode_class_object;
    uint8_t *num_players_property;
    int player_count = 0;
    int target_count;
    int target_index;
    int property_count;
    ULONGLONG now = GetTickCount64();

    if (InterlockedCompareExchange(&g_status_state, 0, 0) != 0) {
        return;
    }
    target_count = collect_runtime_targets(
        world,
        g_targets,
        MAX_RUNTIME_TARGETS,
        &player_count
    );
    game_mode_class_object = game_mode == NULL
        ? NULL
        : *(void **)((uint8_t *)game_mode + UOBJECT_CLASS_PRIVATE);
    if (game_mode_class_object != NULL) {
        fname_to_utf8(
            (const FName *)((uint8_t *)game_mode_class_object + UOBJECT_NAME_PRIVATE),
            game_mode_class,
            sizeof(game_mode_class)
        );
    }
    property_count = count_class_properties(game_mode);
    num_players_property = find_property(game_mode, "NumPlayers");
    export_property_utf8(
        num_players_property,
        game_mode,
        0,
        num_players,
        sizeof(num_players)
    );
    json_init(&builder, g_status_buffer, sizeof(g_status_buffer));
    json_append(
        &builder,
        "{\"version\":1,\"ready\":true,\"player_controller_count\":"
    );
    json_append_format(
        &builder,
        "%d,\"game_mode_class\":",
        player_count
    );
    json_append_string(&builder, game_mode_class);
    json_append_format(
        &builder,
        ",\"game_mode_property_count\":%d,\"game_mode_num_players\":",
        property_count
    );
    json_append_string(&builder, num_players);
    json_append_format(
        &builder,
        ",\"target_count\":%d,\"targets\":[",
        target_count
    );
    for (target_index = 0; target_index < target_count; ++target_index) {
        if (target_index > 0) {
            json_append(&builder, ",");
        }
        append_target_json(&builder, &g_targets[target_index]);
    }
    json_append_format(
        &builder,
        "],\"monotonic_ms\":%llu}\n",
        (unsigned long long)now
    );
    if (builder.failed) {
        return;
    }
    g_status_length = builder.length;
    InterlockedExchange(&g_status_state, 1);
}

static void __fastcall hooked_world_tick(
    void *world,
    uint8_t tick_type,
    float delta_seconds
)
{
    void *active_world;
    void *game_mode;
    ULONGLONG now;

    g_original_world_tick(world, tick_type, delta_seconds);

    if (world == NULL || g_image_base == NULL) {
        return;
    }
    active_world = *(void **)(g_image_base + RVA_GWORLD);
    if (active_world != world) {
        return;
    }
    process_bridge_request(world);
    game_mode = *(void **)((uint8_t *)world + UWORLD_AUTHORITY_GAME_MODE);
    if (game_mode == NULL) {
        return;
    }

    now = GetTickCount64();
    if (now - g_last_sample_ms < 1000) {
        return;
    }
    g_last_sample_ms = now;
    write_status(world, game_mode);
}

static BOOL install_world_tick_hook(void)
{
    static const uint8_t expected_prologue[15] = {
        0x48, 0x8B, 0xC4, 0x55, 0x53,
        0x56, 0x57, 0x41, 0x54, 0x41,
        0x55, 0x41, 0x56, 0x41, 0x57,
    };
    static const uint8_t expected_inventory_member_prologue[10] = {
        0x48, 0x89, 0x5C, 0x24, 0x08,
        0x57, 0x48, 0x83, 0xEC, 0x20,
    };
    static const uint8_t expected_get_inventory_prologue[6] = {
        0x40, 0x53, 0x48, 0x83, 0xEC, 0x20,
    };
    static const uint8_t expected_get_rank_prologue[15] = {
        0x48, 0x89, 0x5C, 0x24, 0x08,
        0x48, 0x89, 0x74, 0x24, 0x10,
        0x57, 0x48, 0x83, 0xEC, 0x20,
    };
    uint8_t *target;
    uint8_t *trampoline;
    uint8_t patch[15];
    DWORD old_protection;
    DWORD ignored;
    HANDLE worker_thread;
    IMAGE_DOS_HEADER *dos_header;
    IMAGE_NT_HEADERS64 *nt_headers;
    uint64_t destination;

    g_image_base = (uint8_t *)GetModuleHandleW(NULL);
    if (g_image_base == NULL) {
        append_log("bridge disabled: main image base unavailable");
        return FALSE;
    }
    dos_header = (IMAGE_DOS_HEADER *)g_image_base;
    if (dos_header->e_magic != IMAGE_DOS_SIGNATURE ||
        dos_header->e_lfanew <= 0 ||
        dos_header->e_lfanew > 0x100000) {
        append_log("bridge disabled: invalid main image headers");
        return FALSE;
    }
    nt_headers = (IMAGE_NT_HEADERS64 *)(
        g_image_base + (uint32_t)dos_header->e_lfanew
    );
    if (nt_headers->Signature != IMAGE_NT_SIGNATURE ||
        nt_headers->FileHeader.TimeDateStamp != UINT32_C(0x67188699) ||
        nt_headers->OptionalHeader.SizeOfImage != UINT32_C(0x05810000)) {
        append_log("bridge disabled: unsupported server executable build");
        return FALSE;
    }
    g_fname_to_string = (FNameToStringFn)(uintptr_t)(
        g_image_base + RVA_FNAME_TO_STRING
    );
    g_fstring_destructor = (FStringDestructorFn)(uintptr_t)(
        g_image_base + RVA_FSTRING_DESTRUCTOR
    );
    g_property_import_text = (FPropertyImportTextFn)(uintptr_t)(
        g_image_base + RVA_FPROPERTY_IMPORT_TEXT
    );
    g_flush_net_dormancy = (AActorFlushNetDormancyFn)(uintptr_t)(
        g_image_base + RVA_AACTOR_FLUSH_NET_DORMANCY
    );
    g_force_net_update = (AActorForceNetUpdateFn)(uintptr_t)(
        g_image_base + RVA_AACTOR_FORCE_NET_UPDATE
    );
    if (memcmp(
            g_image_base + RVA_MORDHAU_INVENTORY_GET_PLAYER_XP,
            expected_inventory_member_prologue,
            sizeof(expected_inventory_member_prologue)) == 0 &&
        memcmp(
            g_image_base + RVA_MORDHAU_INVENTORY_IS_AVAILABLE,
            expected_inventory_member_prologue,
            sizeof(expected_inventory_member_prologue)) == 0 &&
        memcmp(
            g_image_base + RVA_MORDHAU_UTILITY_GET_INVENTORY,
            expected_get_inventory_prologue,
            sizeof(expected_get_inventory_prologue)) == 0 &&
        memcmp(
            g_image_base + RVA_MORDHAU_UTILITY_GET_RANK_FROM_XP,
            expected_get_rank_prologue,
            sizeof(expected_get_rank_prologue)) == 0) {
        g_inventory_get_player_xp =
            (MordhauInventoryGetPlayerXPFn)(uintptr_t)(
                g_image_base + RVA_MORDHAU_INVENTORY_GET_PLAYER_XP
            );
        g_inventory_is_available =
            (MordhauInventoryIsAvailableFn)(uintptr_t)(
                g_image_base + RVA_MORDHAU_INVENTORY_IS_AVAILABLE
            );
        g_utility_get_inventory =
            (MordhauUtilityGetInventoryFn)(uintptr_t)(
                g_image_base + RVA_MORDHAU_UTILITY_GET_INVENTORY
            );
        g_utility_get_rank_from_xp =
            (MordhauUtilityGetRankFromXPFn)(uintptr_t)(
                g_image_base + RVA_MORDHAU_UTILITY_GET_RANK_FROM_XP
            );
    } else {
        append_log(
            "account progress unavailable: pinned function prologue mismatch"
        );
    }
    g_response_event = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (g_response_event == NULL) {
        append_log("bridge disabled: response event creation failed");
        return FALSE;
    }
    target = g_image_base + RVA_UWORLD_TICK;
    if (memcmp(target, expected_prologue, sizeof(expected_prologue)) != 0) {
        append_log("bridge disabled: UWorld::Tick prologue does not match");
        CloseHandle(g_response_event);
        g_response_event = NULL;
        return FALSE;
    }

    trampoline = (uint8_t *)VirtualAlloc(
        NULL,
        64,
        MEM_COMMIT | MEM_RESERVE,
        PAGE_EXECUTE_READWRITE
    );
    if (trampoline == NULL) {
        append_log("bridge disabled: trampoline allocation failed");
        return FALSE;
    }
    memcpy(trampoline, target, sizeof(expected_prologue));
    trampoline[15] = 0xFF;
    trampoline[16] = 0x25;
    memset(trampoline + 17, 0, 4);
    destination = (uint64_t)(uintptr_t)(target + sizeof(expected_prologue));
    memcpy(trampoline + 21, &destination, sizeof(destination));
    FlushInstructionCache(GetCurrentProcess(), trampoline, 29);
    g_original_world_tick = (UWorldTickFn)(uintptr_t)trampoline;

    memset(patch, 0x90, sizeof(patch));
    patch[0] = 0xFF;
    patch[1] = 0x25;
    memset(patch + 2, 0, 4);
    destination = (uint64_t)(uintptr_t)&hooked_world_tick;
    memcpy(patch + 6, &destination, sizeof(destination));

    if (!VirtualProtect(
            target,
            sizeof(patch),
            PAGE_EXECUTE_READWRITE,
            &old_protection)) {
        VirtualFree(trampoline, 0, MEM_RELEASE);
        append_log("bridge disabled: VirtualProtect failed");
        return FALSE;
    }
    memcpy(target, patch, sizeof(patch));
    FlushInstructionCache(GetCurrentProcess(), target, sizeof(patch));
    VirtualProtect(target, sizeof(patch), old_protection, &ignored);

    worker_thread = CreateThread(
        NULL,
        0,
        bridge_worker_thread,
        NULL,
        0,
        NULL
    );
    if (worker_thread == NULL) {
        append_log("runtime bridge installed without IPC worker");
        return TRUE;
    }
    CloseHandle(worker_thread);
    append_log("runtime bridge installed");
    return TRUE;
}

static BOOL CALLBACK initialize_bridge(PINIT_ONCE once, PVOID parameter, PVOID *context)
{
    (void)once;
    (void)parameter;
    (void)context;
    return install_world_tick_hook();
}

static DWORD WINAPI bridge_start_thread(LPVOID parameter)
{
    (void)parameter;
    Sleep(100);
    InitOnceExecuteOnce(&g_bridge_once, initialize_bridge, NULL, NULL);
    return 0;
}

__declspec(dllexport)
HRESULT WINAPI CreateDXGIFactory(REFIID riid, void **factory)
{
    if (!InitOnceExecuteOnce(&g_proxy_once, initialize_proxy, NULL, NULL) ||
        g_real_create_factory == NULL) {
        return E_FAIL;
    }
    InitOnceExecuteOnce(&g_bridge_once, initialize_bridge, NULL, NULL);
    return g_real_create_factory(riid, factory);
}

BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID reserved)
{
    HANDLE thread;

    (void)reserved;
    if (reason == DLL_PROCESS_ATTACH) {
        DisableThreadLibraryCalls(instance);
        thread = CreateThread(NULL, 0, bridge_start_thread, NULL, 0, NULL);
        if (thread != NULL) {
            CloseHandle(thread);
        }
    }
    return TRUE;
}
